package jira_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

const windowJQL = `project = "PLAT" AND (updated >= "-30d" OR (resolution = Unresolved AND updated >= "-180d")) ORDER BY key`

type sleeper struct{ slept []time.Duration }

func (s *sleeper) sleep(_ context.Context, d time.Duration) error {
	s.slept = append(s.slept, d)
	return nil
}

func newClient(s *jiramock.Server) (*jira.Client, *sleeper) {
	sl := &sleeper{}
	return &jira.Client{BaseURL: s.URL, Token: s.Token, Sleep: sl.sleep}, sl
}

func putIssues(s *jiramock.Server, keys ...string) {
	for i, k := range keys {
		s.Put(jiramock.NewIssue(k, 10000+i, jiramock.DefaultNow))
	}
}

func issueKeys(issues []jira.Issue) []string {
	var keys []string
	for _, iss := range issues {
		keys = append(keys, iss.Key)
	}
	return keys
}

// requestLog renders each request as "path?query" with sorted query keys.
func requestLog(s *jiramock.Server) []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Path+"?"+r.Query.Encode())
	}
	return out
}

func searchQuery(start int) string {
	q := url.Values{"jql": {windowJQL}, "fields": {"key"}, "maxResults": {"1000"}, "startAt": {strconv.Itoa(start)}}
	return "/rest/api/2/search?" + q.Encode()
}

func wantLog(t *testing.T, s *jiramock.Server, want ...string) {
	t.Helper()
	if got := requestLog(s); !slices.Equal(got, want) {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func wantAPIError(t *testing.T, err error, status int) jira.APIError {
	t.Helper()
	var apiErr *jira.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
		t.Fatalf("err = %v, want *APIError %d", err, status)
	}
	return *apiErr
}

func TestMyselfSendsTokenAndAcceptHeader(t *testing.T) {
	s := jiramock.New(t)
	c := &jira.Client{BaseURL: s.URL, Token: " test-token\n"}

	u, err := c.Myself(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (jira.User{Name: "svc-jira-cache", Key: "JIRAUSER10500", DisplayName: "svc-jira-cache", Active: true}); u != want {
		t.Errorf("User = %+v, want %+v", u, want)
	}
	r := s.Requests()[0]
	if r.Method != http.MethodGet || r.Path != "/rest/api/2/myself" ||
		r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" {
		t.Errorf("request = %+v", r)
	}
}

func TestBaseURLMayHaveAContextPath(t *testing.T) {
	s := jiramock.New(t)
	c := &jira.Client{BaseURL: s.URL + "/jira", Token: s.Token, HTTP: &http.Client{Transport: roundTripper(
		func(r *http.Request) (*http.Response, error) {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/jira")
			return http.DefaultTransport.RoundTrip(r)
		})}}

	if _, err := c.Myself(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServerInfoIsReadAnonymously(t *testing.T) {
	for _, deployment := range []string{"Server", "DataCenter"} {
		s := jiramock.New(t)
		s.DeploymentType = deployment
		c := &jira.Client{BaseURL: s.URL, Token: "wrong"}

		info, err := c.ServerInfo(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if info.BaseURL != s.URL || info.Version != "10.3.4" || !slices.Equal(info.VersionNumbers, []int{10, 3, 4}) ||
			info.DeploymentType != deployment || info.ServerTitle != "Jira" {
			t.Errorf("ServerInfo = %+v", info)
		}
	}
}

func TestFields(t *testing.T) {
	s := jiramock.New(t)
	c, _ := newClient(s)

	fields, err := c.Fields(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(fields, func(f jira.Field) bool { return f.ID == "customfield_10100" })
	if len(fields) != len(s.Fields) || i < 0 || fields[i].Name != "Sprint" ||
		fields[i].Schema.Custom != "com.pyxis.greenhopper.jira:gh-sprint" {
		t.Errorf("Fields = %+v", fields)
	}
}

func TestAnonymousResponsesAreUnauthenticated(t *testing.T) {
	calls := map[string]func(context.Context, *jira.Client) error{
		"/rest/api/2/myself": func(ctx context.Context, c *jira.Client) error { _, err := c.Myself(ctx); return err },
		"/rest/api/2/field":  func(ctx context.Context, c *jira.Client) error { _, err := c.Fields(ctx); return err },
		"/rest/api/2/search": func(ctx context.Context, c *jira.Client) error {
			_, err := c.SearchPage(ctx, windowJQL, nil, 0, 1)
			return err
		},
		"/rest/api/2/issue/PLAT-1": func(ctx context.Context, c *jira.Client) error {
			_, err := c.GetIssue(ctx, "PLAT-1", nil)
			return err
		},
		"/rest/api/2/issue/PLAT-1/comment": func(ctx context.Context, c *jira.Client) error {
			_, err := c.Comments(ctx, "PLAT-1")
			return err
		},
	}
	for _, anon := range []jiramock.AnonymousSearch{jiramock.AnonEmpty, jiramock.AnonFieldHidden} {
		for path, call := range calls {
			s := jiramock.New(t)
			s.AnonymousSearch = anon
			putIssues(s, "PLAT-1")
			c, _ := newClient(s)
			c.Token = "expired"

			err := call(t.Context(), c)

			if !errors.Is(err, jira.ErrUnauthenticated) || !strings.HasPrefix(err.Error(), "GET "+path+": 401 Unauthorized") {
				t.Errorf("%s: err = %v, want ErrUnauthenticated", path, err)
			}
			if n := len(s.Requests()); n != 1 {
				t.Errorf("%s: %d requests, want 1", path, n)
			}
		}
	}
}

func TestMissingUserHeaderIsNotAnonymous(t *testing.T) {
	s := jiramock.New(t)
	s.OmitUserHeader = true
	putIssues(s, "PLAT-1")
	c, _ := newClient(s)
	c.Token = "expired"

	page, err := c.SearchPage(t.Context(), windowJQL, nil, 0, 1)
	if err != nil || page.Total != 0 {
		t.Errorf("SearchPage = %+v, %v; want an empty page", page, err)
	}
	if _, err := c.Myself(t.Context()); !errors.Is(err, jira.ErrUnauthenticated) {
		t.Errorf("Myself err = %v, want ErrUnauthenticated", err)
	}
}

func TestServerInfo401IsUnauthenticated(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusUnauthorized})
	c, _ := newClient(s)

	if _, err := c.ServerInfo(t.Context()); !errors.Is(err, jira.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestSearchPage(t *testing.T) {
	s := jiramock.New(t)
	putIssues(s, "PLAT-1", "PLAT-2", "PLAT-3")
	c, _ := newClient(s)

	page, err := c.SearchPage(t.Context(), windowJQL, []string{"updated", "summary"}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.StartAt != 1 || page.MaxResults != 1 || page.Total != 3 || len(page.Issues) != 1 {
		t.Fatalf("page = %+v", page)
	}
	iss := page.Issues[0]
	if iss.Key != "PLAT-2" || iss.ID != "10001" || len(iss.Fields) != 2 ||
		string(iss.Fields["summary"]) != `"Issue PLAT-2"` {
		t.Errorf("issue = %+v", iss)
	}

	if _, err := c.SearchPage(t.Context(), windowJQL, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	q := s.Requests()[1].Query
	if q.Get("startAt") != "0" || q.Get("maxResults") != "0" || q.Has("fields") || q.Get("jql") != windowJQL {
		t.Errorf("query = %v", q)
	}
}

func TestSearchPageBadJQLIsNotRetried(t *testing.T) {
	s := jiramock.New(t)
	c, _ := newClient(s)

	_, err := c.SearchPage(t.Context(), "nonsense", nil, 0, 1)

	apiErr := wantAPIError(t, err, http.StatusBadRequest)
	if apiErr.Method != http.MethodGet || apiErr.Path != "/rest/api/2/search" ||
		!slices.Equal(apiErr.Messages, []string{"Error in the JQL Query: unsupported by jiramock: nonsense"}) {
		t.Errorf("APIError = %+v", apiErr)
	}
	if want := "GET /rest/api/2/search: 400: Error in the JQL Query: unsupported by jiramock: nonsense"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if n := len(s.Requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestAPIErrorFieldErrors(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusBadRequest, Body: `{"errorMessages":["m"],"errors":{"jql":"bad","b":"x"}}`})
	c, _ := newClient(s)

	_, err := c.Fields(t.Context())

	apiErr := wantAPIError(t, err, http.StatusBadRequest)
	if apiErr.FieldErrors["jql"] != "bad" || len(apiErr.FieldErrors) != 2 {
		t.Errorf("FieldErrors = %v", apiErr.FieldErrors)
	}
	if want := "GET /rest/api/2/field: 400: m; b: x; jql: bad"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestAPIErrorWithoutMessagesShowsStatusText(t *testing.T) {
	err := &jira.APIError{Method: "GET", Path: "/p", StatusCode: 503}
	if want := "GET /p: 503: Service Unavailable"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestGetIssue(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.ExampleIssue())
	c, _ := newClient(s)

	iss, err := c.GetIssue(t.Context(), "PLAT-123", []string{"summary", "customfield_10002"})
	if err != nil {
		t.Fatal(err)
	}
	if iss.ID != "10230" || iss.Key != "PLAT-123" || len(iss.Fields) != 2 || string(iss.Fields["customfield_10002"]) != "3" {
		t.Errorf("issue = %+v", iss)
	}
	if q := s.Requests()[0].Query; q.Get("fields") != "summary,customfield_10002" {
		t.Errorf("query = %v", q)
	}

	all, err := c.GetIssue(t.Context(), "PLAT-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if q := s.Requests()[1].Query; q.Has("fields") || len(all.Fields) != len(jiramock.ExampleIssue().Fields) {
		t.Errorf("query = %v, %d fields", q, len(all.Fields))
	}
}

func TestGetIssueNotFound(t *testing.T) {
	s := jiramock.New(t)
	c, _ := newClient(s)

	_, err := c.GetIssue(t.Context(), "PLAT-9", nil)

	apiErr := wantAPIError(t, err, http.StatusNotFound)
	if apiErr.Path != "/rest/api/2/issue/PLAT-9" || !slices.Equal(apiErr.Messages, []string{"Issue Does Not Exist"}) {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestGetIssueEscapesKey(t *testing.T) {
	s := jiramock.New(t)
	putIssues(s, "PLAT-1")
	c, _ := newClient(s)

	_, err := c.GetIssue(t.Context(), "PLAT-1?x", nil)

	wantAPIError(t, err, http.StatusNotFound)
	if p := s.Requests()[0].Path; p != "/rest/api/2/issue/PLAT-1?x" {
		t.Errorf("path = %q", p)
	}
}

func commentIssue(n int) jiramock.Issue {
	iss := jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow)
	for i := range n {
		iss.Comments = append(iss.Comments, map[string]any{
			"id":      strconv.Itoa(40001 + i),
			"author":  map[string]any{"name": "jdoe", "displayName": "Jane Doe", "active": true},
			"body":    fmt.Sprintf("comment %d\r\n", i),
			"created": jiramock.DefaultNow.Add(time.Duration(i) * time.Minute).Format(jiramock.Layout),
			"updated": jiramock.DefaultNow.Format(jiramock.Layout),
		})
	}
	return iss
}

func commentQuery(start int) string {
	return fmt.Sprintf("/rest/api/2/issue/PLAT-1/comment?maxResults=1000&orderBy=created&startAt=%d", start)
}

func TestCommentsPages(t *testing.T) {
	for n, starts := range map[int][]int{0: {0}, 1: {0}, 2: {0}, 4: {0, 2}, 5: {0, 2, 4}} {
		s := jiramock.New(t)
		s.MaxResultsCap = 2
		s.Put(commentIssue(n))
		c, _ := newClient(s)

		got, err := c.Comments(t.Context(), "PLAT-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("%d comments: got %+v", n, got)
		}
		for i, cm := range got {
			if cm.ID != strconv.Itoa(40001+i) || cm.Body != fmt.Sprintf("comment %d\r\n", i) ||
				cm.Author == nil || cm.Author.Name != "jdoe" || cm.Created == "" || cm.Updated == "" {
				t.Errorf("comment %d = %+v", i, cm)
			}
		}
		var want []string
		for _, st := range starts {
			want = append(want, commentQuery(st))
		}
		wantLog(t, s, want...)
	}
}

func TestCommentsStopOnEmptyPage(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	s.Put(commentIssue(4))
	s.OnRequest = func(r jiramock.Request) {
		if r.Query.Get("startAt") == "2" {
			s.Inject(jiramock.Fault{Status: http.StatusOK, Body: `{"startAt":2,"maxResults":2,"total":4,"comments":[]}`, Times: 1})
		}
	}
	c, _ := newClient(s)

	got, err := c.Comments(t.Context(), "PLAT-1")
	if err != nil || len(got) != 2 {
		t.Errorf("Comments = %+v, %v", got, err)
	}
	wantLog(t, s, commentQuery(0), commentQuery(2))
}

func TestCommentsError(t *testing.T) {
	s := jiramock.New(t)
	c, _ := newClient(s)

	_, err := c.Comments(t.Context(), "PLAT-1")

	wantAPIError(t, err, http.StatusNotFound)
}

func TestSearchAllPages(t *testing.T) {
	for n, starts := range map[int][]int{0: {0}, 1: {0}, 4: {0, 2}, 5: {0, 2, 4}} {
		s := jiramock.New(t)
		s.MaxResultsCap = 2
		s.Projects = []string{"PLAT"}
		var keys []string
		for i := range n {
			keys = append(keys, fmt.Sprintf("PLAT-%d", i+1))
		}
		putIssues(s, keys...)
		c, _ := newClient(s)

		got, err := c.SearchAll(t.Context(), windowJQL, []string{"key"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(issueKeys(got), keys) {
			t.Errorf("%d issues: keys = %v", n, issueKeys(got))
		}
		var want []string
		for _, st := range starts {
			want = append(want, searchQuery(st))
		}
		wantLog(t, s, want...)
	}
}

// shiftOnSecondPage adds an issue before each of the first n requests for a second page.
func shiftOnSecondPage(s *jiramock.Server, n int) {
	next := 100
	s.OnRequest = func(r jiramock.Request) {
		if r.Query.Get("startAt") == "2" && n > 0 {
			n--
			next++
			putIssues(s, fmt.Sprintf("PLAT-%d", next))
		}
	}
}

func TestSearchAllRestartsWhenTotalShifts(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	putIssues(s, "PLAT-1", "PLAT-2", "PLAT-3")
	shiftOnSecondPage(s, 2)
	c, _ := newClient(s)

	got, err := c.SearchAll(t.Context(), windowJQL, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PLAT-1", "PLAT-2", "PLAT-3", "PLAT-101", "PLAT-102"}; !slices.Equal(issueKeys(got), want) {
		t.Errorf("keys = %v, want %v", issueKeys(got), want)
	}
	wantLog(t, s, searchQuery(0), searchQuery(2), searchQuery(0), searchQuery(2),
		searchQuery(0), searchQuery(2), searchQuery(4))
}

func TestSearchAllGivesUpAfterThreeShifts(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	putIssues(s, "PLAT-1", "PLAT-2", "PLAT-3")
	shiftOnSecondPage(s, 3)
	c, _ := newClient(s)

	_, err := c.SearchAll(t.Context(), windowJQL, []string{"key"})

	if !errors.Is(err, jira.ErrWindowShifted) {
		t.Fatalf("err = %v, want ErrWindowShifted", err)
	}
	wantLog(t, s, searchQuery(0), searchQuery(2), searchQuery(0), searchQuery(2), searchQuery(0), searchQuery(2))
}

func TestSearchAllRestartsOnEarlyEmptyPage(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	putIssues(s, "PLAT-1", "PLAT-2", "PLAT-3")
	first := true
	s.OnRequest = func(r jiramock.Request) {
		if r.Query.Get("startAt") == "2" && first {
			first = false
			s.Inject(jiramock.Fault{Status: http.StatusOK, Body: `{"startAt":2,"maxResults":2,"total":3,"issues":[]}`, Times: 1})
		}
	}
	c, _ := newClient(s)

	got, err := c.SearchAll(t.Context(), windowJQL, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PLAT-1", "PLAT-2", "PLAT-3"}; !slices.Equal(issueKeys(got), want) {
		t.Errorf("keys = %v, want %v", issueKeys(got), want)
	}
	wantLog(t, s, searchQuery(0), searchQuery(2), searchQuery(0), searchQuery(2))
}

func TestSearchAllDedupesByKey(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	putIssues(s, "PLAT-2", "PLAT-3", "PLAT-4", "PLAT-5")
	s.OnRequest = func(r jiramock.Request) {
		if r.Query.Get("startAt") == "2" {
			s.Delete("PLAT-5")
			putIssues(s, "PLAT-1")
		}
	}
	c, _ := newClient(s)

	got, err := c.SearchAll(t.Context(), windowJQL, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PLAT-2", "PLAT-3", "PLAT-4"}; !slices.Equal(issueKeys(got), want) {
		t.Errorf("keys = %v, want %v", issueKeys(got), want)
	}
}

func TestSearchAllReturnsErrors(t *testing.T) {
	for _, page := range []int{0, 1} {
		s := jiramock.New(t)
		s.MaxResultsCap = 2
		putIssues(s, "PLAT-1", "PLAT-2", "PLAT-3")
		s.OnRequest = func(r jiramock.Request) {
			if r.Query.Get("startAt") == strconv.Itoa(2*page) {
				s.Inject(jiramock.Fault{Status: http.StatusBadRequest})
			}
		}
		c, _ := newClient(s)

		got, err := c.SearchAll(t.Context(), windowJQL, nil)

		wantAPIError(t, err, http.StatusBadRequest)
		if got != nil || len(s.Requests()) != page+1 {
			t.Errorf("page %d: got %v after %d requests", page, got, len(s.Requests()))
		}
	}
}

func TestRetryAfter(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"":                              time.Second,
		"0":                             time.Second,
		"-3":                            time.Second,
		"Wed, 21 Oct 2026 07:28:00 GMT": time.Second,
		"1":                             time.Second,
		"5":                             5 * time.Second,
		"60":                            60 * time.Second,
		"61":                            60 * time.Second,
		"99999999999999999":             60 * time.Second,
	} {
		s := jiramock.New(t)
		f := jiramock.Fault{Status: http.StatusTooManyRequests, Times: 1}
		if header != "" {
			f.Header = http.Header{"Retry-After": {header}}
		}
		s.Inject(f)
		c, sl := newClient(s)

		if _, err := c.Myself(t.Context()); err != nil {
			t.Fatalf("Retry-After %q: %v", header, err)
		}
		if !slices.Equal(sl.slept, []time.Duration{want}) {
			t.Errorf("Retry-After %q: slept %v, want [%v]", header, sl.slept, want)
		}
	}
}

func TestRetriesOnlyRateLimitsAndServerErrors(t *testing.T) {
	for status, retried := range map[int]bool{429: true, 500: true, 503: true, 499: false, 404: false, 400: false} {
		s := jiramock.New(t)
		s.Inject(jiramock.Fault{Status: status, Times: 1})
		c, _ := newClient(s)

		_, err := c.Myself(t.Context())

		requests := 1
		if retried {
			requests = 2
		}
		if retried != (err == nil) || len(s.Requests()) != requests {
			t.Errorf("%d: err = %v after %d requests", status, err, len(s.Requests()))
		}
	}
}

func TestBackoffDoublesUpToCap(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusServiceUnavailable})
	c, sl := newClient(s)
	c.MaxAttempts = 8

	_, err := c.Myself(t.Context())

	wantAPIError(t, err, http.StatusServiceUnavailable)
	if want := "after 8 attempts: GET /rest/api/2/myself: 503: Service Unavailable"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i := range want {
		want[i] *= time.Second
	}
	if !slices.Equal(sl.slept, want) {
		t.Errorf("slept %v, want %v", sl.slept, want)
	}
	if n := len(s.Requests()); n != 8 {
		t.Errorf("%d requests, want 8", n)
	}
}

func TestDefaultMaxAttemptsIsFive(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusInternalServerError})
	c, _ := newClient(s)

	_, err := c.Fields(t.Context())

	if !strings.HasPrefix(err.Error(), "after 5 attempts: ") || len(s.Requests()) != 5 {
		t.Errorf("err = %v after %d requests", err, len(s.Requests()))
	}
}

func TestSleepErrorStopsRetries(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusServiceUnavailable})
	c, _ := newClient(s)
	c.Sleep = func(context.Context, time.Duration) error { return context.Canceled }

	_, err := c.Myself(t.Context())

	if !errors.Is(err, context.Canceled) || len(s.Requests()) != 1 {
		t.Errorf("err = %v after %d requests", err, len(s.Requests()))
	}
}

func TestDefaultSleepWaits(t *testing.T) {
	t.Parallel()
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusServiceUnavailable, Times: 1})
	c := &jira.Client{BaseURL: s.URL, Token: s.Token}

	start := time.Now()
	if _, err := c.Myself(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Second {
		t.Errorf("retried after %v, want 1s", d)
	}
}

func TestDefaultSleepEndsWithContext(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusServiceUnavailable})
	c := &jira.Client{BaseURL: s.URL, Token: s.Token}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Myself(ctx)

	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 900*time.Millisecond {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// failingClient fails the first n round trips with err, then uses the default transport.
func failingClient(n int, err error) *http.Client {
	return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		if n > 0 {
			n--
			return nil, err
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
}

func TestRetriesTransientTransportErrors(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	for _, transient := range []error{io.ErrUnexpectedEOF, reset} {
		s := jiramock.New(t)
		c, sl := newClient(s)
		c.HTTP = failingClient(2, transient)

		if _, err := c.Myself(t.Context()); err != nil {
			t.Errorf("%v: %v", transient, err)
		}
		if len(sl.slept) != 2 {
			t.Errorf("%v: slept %v", transient, sl.slept)
		}
	}
}

func TestOtherTransportErrorsAreNotRetried(t *testing.T) {
	s := jiramock.New(t)
	c, sl := newClient(s)
	c.HTTP = failingClient(1, errors.New("x509: certificate signed by unknown authority"))

	_, err := c.Myself(t.Context())

	if err == nil || !strings.Contains(err.Error(), "x509: certificate signed by unknown authority") || len(sl.slept) != 0 {
		t.Errorf("err = %v, slept %v", err, sl.slept)
	}
}

type truncatedBody struct{}

func (truncatedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (truncatedBody) Close() error             { return nil }

func TestRetriesTruncatedBody(t *testing.T) {
	s := jiramock.New(t)
	c, sl := newClient(s)
	truncated := 1
	c.HTTP = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && truncated > 0 {
			truncated--
			_ = res.Body.Close()
			res.Body = truncatedBody{}
		}
		return res, err
	})}

	if _, err := c.Myself(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sl.slept) != 1 {
		t.Errorf("slept %v", sl.slept)
	}
}

func TestNonJSONSuccessNamesContentType(t *testing.T) {
	login := jiramock.Fault{Path: "/login.jsp", Status: http.StatusOK}
	redirect := jiramock.Fault{Path: "/rest/", Status: http.StatusFound, Header: http.Header{"Location": {"/login.jsp"}}}
	for _, faults := range [][]jiramock.Fault{{{Status: http.StatusOK}}, {redirect, login}} {
		s := jiramock.New(t)
		for _, f := range faults {
			s.Inject(f)
		}
		c, sl := newClient(s)

		_, err := c.Myself(t.Context())

		want := `GET /rest/api/2/myself: response is not JSON (Content-Type "text/html;charset=UTF-8")`
		if err == nil || !strings.HasPrefix(err.Error(), want) || len(sl.slept) != 0 {
			t.Errorf("err = %v, want prefix %q", err, want)
		}
	}
}

func TestBadBaseURL(t *testing.T) {
	c := &jira.Client{BaseURL: "http://bad host"}
	if _, err := c.Myself(t.Context()); err == nil {
		t.Error("err = nil")
	}
}
