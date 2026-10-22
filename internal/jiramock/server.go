package jiramock

import (
	"cmp"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const notAuthenticated = "You are not authenticated. Authentication required to perform this operation."

// Fault makes matching requests fail instead of being served.
type Fault struct {
	Path   string      // URL path prefix to match; "" matches every path
	Status int         // e.g. 429, 500, 503, 302, or 200 for an HTML login page
	Header http.Header // added to the response; overrides the defaults
	Body   string      // "" uses the default body for Status
	Times  int         // matching requests to fail, then the fault is removed; <= 0 means forever
}

// Request is one request the server received.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
}

// AnonymousSearch selects how a search without a valid token is answered.
type AnonymousSearch int

// The ways an anonymous search is answered.
const (
	AnonEmpty       AnonymousSearch = iota // 200, total 0
	AnonFieldHidden                        // 400: issuetype cannot be viewed by anonymous users
	AnonNoProject                          // 400: the first project does not exist
)

// Server is a fake Jira. Set its exported fields before the first request; New fills in the defaults shown.
type Server struct {
	URL string // base URL, no trailing slash

	Token           string            // "test-token"; matching "Authorization: Bearer <Token>" authenticates
	User            string            // "svc-jira-cache"
	DeploymentType  string            // "Server"
	Version         string            // "10.3.4"
	MaxResultsCap   int               // 1000; server-side cap for search and comment pages
	Projects        []string          // extra known projects; projects of stored issues are always known
	Aliases         map[string]string // old project key → current key, for renamed projects
	Fields          []map[string]any  // GET /field body; ExampleFields()
	Now             func() time.Time  // returns DefaultNow
	OmitUserHeader  bool              // drop X-AUSERNAME, as a stripping proxy would
	AnonymousSearch AnonymousSearch   // how an anonymous search is answered
	// OnRequest runs before each request is evaluated, without the lock held.
	// It may call Put or Delete (to shift total between pages) or block (to observe concurrency).
	OnRequest func(Request)

	mu          sync.Mutex
	issues      map[string]stored
	putProjects map[string]bool
	faults      []Fault
	requests    []Request
}

type stored struct {
	Issue
	updated  time.Time
	comments []map[string]any // by created, then numeric id
}

// New starts a server with default settings. It is closed when t ends.
func New(t testing.TB) *Server {
	s := &Server{
		Token:          "test-token",
		User:           "svc-jira-cache",
		DeploymentType: "Server",
		Version:        "10.3.4",
		MaxResultsCap:  1000,
		Fields:         ExampleFields(),
		Now:            func() time.Time { return DefaultNow },
		issues:         map[string]stored{},
		putProjects:    map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/2/serverInfo", s.serverInfo)
	mux.HandleFunc("GET /rest/api/2/myself", s.myself)
	mux.HandleFunc("GET /rest/api/2/field", s.fields)
	mux.HandleFunc("GET /rest/api/2/search", s.search)
	mux.HandleFunc("GET /rest/api/2/issue/{key}", s.issue)
	mux.HandleFunc("GET /rest/api/2/issue/{key}/comment", s.comments)
	ts := httptest.NewServer(s.serve(mux))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// Put stores copies of issues, inserting or replacing by Key. It panics on a time that is not in Layout.
func (s *Server) Put(issues ...Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range issues {
		i = deepCopy(i)
		s.issues[i.Key] = stored{Issue: i, updated: mustTime(i.Key, i.Fields["updated"]), comments: sortComments(i)}
		s.putProjects[projectOf(i.Key)] = true
	}
}

// Delete removes issues by key.
func (s *Server) Delete(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.issues, k)
	}
}

// Inject adds a fault after any already injected.
func (s *Server) Inject(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, f)
}

// Requests returns a copy of every request served, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) serve(mux http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		if s.OnRequest != nil {
			s.OnRequest(req)
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		if !s.OmitUserHeader {
			w.Header().Set("X-AUSERNAME", s.userName(r))
		}
		if !s.serveFault(w, r.URL.Path) {
			mux.ServeHTTP(w, r)
		}
	}
}

func (s *Server) authenticated(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+s.Token
}

func (s *Server) userName(r *http.Request) string {
	if s.authenticated(r) {
		return s.User
	}
	return "anonymous"
}

func (s *Server) serveFault(w http.ResponseWriter, path string) bool {
	i := slices.IndexFunc(s.faults, func(f Fault) bool { return strings.HasPrefix(path, f.Path) })
	if i < 0 {
		return false
	}
	f := s.faults[i]
	s.faults[i].Times--
	if s.faults[i].Times == 0 {
		s.faults = slices.Delete(s.faults, i, i+1)
	}

	h := w.Header()
	body := "<html><body>Log in</body></html>"
	if f.Status >= http.StatusInternalServerError {
		body = "<html><body>Internal Server Error</body></html>"
	}
	if f.Status == http.StatusTooManyRequests {
		h.Set("X-RateLimit-Limit", "100")
		h.Set("X-RateLimit-Remaining", "0")
		h.Set("X-RateLimit-Interval-Seconds", "1")
		h.Set("X-RateLimit-FillRate", "10")
		body = "{}"
	} else {
		h.Set("Content-Type", "text/html;charset=UTF-8")
	}
	for k, v := range f.Header {
		h[http.CanonicalHeaderKey(k)] = v
	}
	w.WriteHeader(f.Status)
	_, _ = io.WriteString(w, cmp.Or(f.Body, body))
	return true
}

func (s *Server) serverInfo(w http.ResponseWriter, _ *http.Request) {
	var major, minor, patch int
	_, _ = fmt.Sscanf(s.Version, "%d.%d.%d", &major, &minor, &patch)
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl":        s.URL,
		"version":        s.Version,
		"versionNumbers": []int{major, minor, patch},
		"deploymentType": s.DeploymentType,
		"buildNumber":    major*1_000_000 + minor*10_000 + patch,
		"serverTime":     s.Now().Format(Layout),
		"serverTitle":    "Jira",
	})
}

func (s *Server) myself(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, notAuthenticated)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"self":        s.URL + "/rest/api/2/user?username=" + url.QueryEscape(s.User),
		"key":         "JIRAUSER10500",
		"name":        s.User,
		"displayName": s.User,
		"active":      true,
		"timeZone":    "Etc/UTC",
	})
}

func (s *Server) fields(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, notAuthenticated)
		return
	}
	writeJSON(w, http.StatusOK, s.Fields)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	jql := q.Get("jql")
	query, ok := parseJQL(jql)
	if !ok {
		writeError(w, http.StatusBadRequest, "Error in the JQL Query: unsupported by jiramock: "+jql)
		return
	}
	start, limit, ok := pageParams(w, q, 50)
	if !ok {
		return
	}

	matched := []stored{}
	if s.authenticated(r) {
		projects := make([]string, 0, len(query.projects))
		for _, p := range query.projects {
			cur := s.current(p)
			if !s.known(cur) {
				writeError(w, http.StatusBadRequest, noProject(p))
				return
			}
			projects = append(projects, cur)
		}
		matched = s.match(query, projects)
	} else if msg := s.anonymousSearchError(query); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	limit = min(limit, s.MaxResultsCap)
	bodies := []issueBody{}
	for _, st := range page(matched, start, limit) {
		bodies = append(bodies, newIssueBody(st.Issue, selectFields(st.Fields, q)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":     "schema,names",
		"startAt":    start,
		"maxResults": limit,
		"total":      len(matched),
		"issues":     bodies,
	})
}

func (s *Server) anonymousSearchError(q query) string {
	switch s.AnonymousSearch {
	case AnonFieldHidden:
		return "Field 'issuetype' does not exist or this field cannot be viewed by anonymous users."
	case AnonNoProject:
		return noProject(q.projects[0])
	}
	return ""
}

func noProject(p string) string {
	return fmt.Sprintf("The value '%s' does not exist for the field 'project'.", p)
}

func (s *Server) current(project string) string {
	return cmp.Or(s.Aliases[project], project)
}

func (s *Server) known(project string) bool {
	return s.putProjects[project] || slices.Contains(s.Projects, project)
}

func (s *Server) match(q query, projects []string) []stored {
	now := s.Now()
	out := []stored{}
	for _, st := range s.issues {
		if slices.Contains(projects, projectOf(st.Key)) && q.keep(st, now) {
			out = append(out, st)
		}
	}
	slices.SortFunc(out, q.order)
	return out
}

func (s *Server) lookup(r *http.Request) (stored, bool) {
	if !s.authenticated(r) {
		return stored{}, false
	}
	p, n, _ := strings.Cut(r.PathValue("key"), "-")
	st, ok := s.issues[s.current(p)+"-"+n]
	return st, ok
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	st, ok := s.lookup(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Issue Does Not Exist")
		return
	}
	writeJSON(w, http.StatusOK, newIssueBody(st.Issue, selectFields(st.Fields, r.URL.Query())))
}

func (s *Server) comments(w http.ResponseWriter, r *http.Request) {
	st, ok := s.lookup(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Issue Does Not Exist")
		return
	}
	q := r.URL.Query()
	start, limit, ok := pageParams(w, q, len(st.comments))
	if !ok {
		return
	}
	cs := slices.Clone(st.comments)
	if q.Get("orderBy") == "-created" {
		slices.Reverse(cs)
	}
	limit = min(limit, s.MaxResultsCap)
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt":    start,
		"maxResults": limit,
		"total":      len(cs),
		"comments":   page(cs, start, limit),
	})
}

// query is a parsed JQL string of one of the two supported grammars.
type query struct {
	projects             []string
	windowed             bool // grammar 1: within the window, by key; grammar 2: everything, by updated desc
	days, unresolvedDays int
}

var (
	windowJQL = regexp.MustCompile(`^project = "([^"]*)" AND \(updated >= "-(\d+)d" OR \(resolution = Unresolved AND updated >= "-(\d+)d"\)\) ORDER BY key$`)
	sampleJQL = regexp.MustCompile(`^project in \(("[^"]*"(?:, "[^"]*")*)\) ORDER BY updated DESC$`)
	quoted    = regexp.MustCompile(`"([^"]*)"`)
)

func parseJQL(jql string) (query, bool) {
	if m := windowJQL.FindStringSubmatch(jql); m != nil {
		return query{projects: []string{m[1]}, windowed: true, days: atoi(m[2]), unresolvedDays: atoi(m[3])}, true
	}
	m := sampleJQL.FindStringSubmatch(jql)
	if m == nil {
		return query{}, false
	}
	var q query
	for _, p := range quoted.FindAllStringSubmatch(m[1], -1) {
		q.projects = append(q.projects, p[1])
	}
	return q, true
}

func (q query) keep(st stored, now time.Time) bool {
	within := func(days int) bool { return !st.updated.Before(now.UTC().AddDate(0, 0, -days)) }
	return !q.windowed || within(q.days) || st.Fields["resolution"] == nil && within(q.unresolvedDays)
}

func (q query) order(a, b stored) int {
	if q.windowed {
		return cmp.Compare(keyNum(a.Key), keyNum(b.Key))
	}
	return cmp.Or(b.updated.Compare(a.updated), cmp.Compare(atoi(b.ID), atoi(a.ID)))
}

func projectOf(key string) string {
	p, _, _ := strings.Cut(key, "-")
	return p
}

func keyNum(key string) int {
	_, n, _ := strings.Cut(key, "-")
	return atoi(n)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func sortComments(i Issue) []map[string]any {
	type entry struct {
		created time.Time
		id      int
		raw     map[string]any
	}
	es := make([]entry, 0, len(i.Comments))
	for _, c := range i.Comments {
		id, _ := c["id"].(string)
		es = append(es, entry{mustTime(i.Key, c["created"]), atoi(id), c})
	}
	slices.SortFunc(es, func(a, b entry) int { return cmp.Or(a.created.Compare(b.created), cmp.Compare(a.id, b.id)) })
	out := []map[string]any{}
	for _, e := range es {
		out = append(out, e.raw)
	}
	return out
}

func mustTime(key string, v any) time.Time {
	s, _ := v.(string)
	t, err := time.Parse(Layout, s)
	if err != nil {
		panic(fmt.Sprintf("jiramock: %s: %v", key, err))
	}
	return t
}

// pageParams reads startAt and maxResults, writing a 400 when either is malformed.
func pageParams(w http.ResponseWriter, q url.Values, defaultMax int) (start, limit int, ok bool) {
	start, ok = intParam(w, q, "startAt", 0)
	if !ok {
		return 0, 0, false
	}
	limit, ok = intParam(w, q, "maxResults", defaultMax)
	return start, limit, ok
}

func intParam(w http.ResponseWriter, q url.Values, name string, def int) (int, bool) {
	if !q.Has(name) {
		return def, true
	}
	n, err := strconv.Atoi(q.Get(name))
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, "jiramock: "+name+" must be a non-negative integer")
		return 0, false
	}
	return n, true
}

func page[T any](items []T, start, limit int) []T {
	start = min(start, len(items))
	return items[start : start+min(limit, len(items)-start)]
}

func selectFields(all map[string]any, q url.Values) map[string]any {
	list := q.Get("fields")
	if !q.Has("fields") || list == "*all" || list == "*navigable" {
		return all
	}
	out := map[string]any{}
	for _, f := range strings.Split(list, ",") {
		if v, ok := all[f]; ok {
			out[f] = v
		}
	}
	return out
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{msg}, "errors": map[string]string{}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_, _ = w.Write(mustJSON(v))
}
