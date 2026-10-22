package jiramock_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

const notAuthenticated = `{"errorMessages":["You are not authenticated. Authentication required to perform this operation."],"errors":{}}`

type response struct {
	status int
	header http.Header
	body   string
}

func get(t *testing.T, s *jiramock.Server, path string) response {
	t.Helper()
	return getAs(t, s, path, "Bearer "+s.Token)
}

func getAs(t *testing.T, s *jiramock.Server, path, authorization string) response {
	t.Helper()
	return do(t, http.DefaultClient, s, path, authorization)
}

func do(t *testing.T, c *http.Client, s *jiramock.Server, path, authorization string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{res.StatusCode, res.Header, string(body)}
}

func (r response) want(t *testing.T, status int, body string) {
	t.Helper()
	if r.status != status || r.body != body {
		t.Errorf("got %d %s\nwant %d %s", r.status, r.body, status, body)
	}
}

func decode[T any](t *testing.T, r response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("%d %s: %v", r.status, r.body, err)
	}
	return v
}

func errorBody(msg string) string {
	return fmt.Sprintf(`{"errorMessages":[%q],"errors":{}}`, msg)
}

type searchPage struct {
	Expand     string
	StartAt    int
	MaxResults int
	Total      int
	Issues     []struct {
		Expand, ID, Self, Key string
		Fields                map[string]any
	}
}

func (p searchPage) keys() []string {
	keys := []string{}
	for _, iss := range p.Issues {
		keys = append(keys, iss.Key)
	}
	return keys
}

func searchPath(jql string, params ...string) string {
	q := url.Values{"jql": {jql}}
	for i := 0; i < len(params); i += 2 {
		q.Set(params[i], params[i+1])
	}
	return "/rest/api/2/search?" + q.Encode()
}

func windowJQL(project string, days, unresolvedDays int) string {
	return fmt.Sprintf(`project = %q AND (updated >= "-%dd" OR (resolution = Unresolved AND updated >= "-%dd")) ORDER BY key`,
		project, days, unresolvedDays)
}

func search(t *testing.T, s *jiramock.Server, jql string, params ...string) searchPage {
	t.Helper()
	r := get(t, s, searchPath(jql, params...))
	if r.status != http.StatusOK {
		t.Fatalf("search %s: %d %s", jql, r.status, r.body)
	}
	return decode[searchPage](t, r)
}

func resolved(iss jiramock.Issue) jiramock.Issue {
	iss.Fields["resolution"] = map[string]any{"name": "Done"}
	return iss
}

func TestServerInfoIsServedToAnyone(t *testing.T) {
	s := jiramock.New(t)
	for _, auth := range []string{"Bearer test-token", ""} {
		r := getAs(t, s, "/rest/api/2/serverInfo", auth)

		r.want(t, http.StatusOK, `{"baseUrl":"`+s.URL+`","buildNumber":10030004,"deploymentType":"Server",`+
			`"serverTime":"2026-10-22T07:00:00.000+0000","serverTitle":"Jira","version":"10.3.4","versionNumbers":[10,3,4]}`)
	}
}

func TestServerInfoUsesSettings(t *testing.T) {
	s := jiramock.New(t)
	s.DeploymentType = "DataCenter"
	s.Version = "11.2.7"
	s.Now = func() time.Time { return time.Date(2027, 1, 2, 3, 4, 5, 6e6, time.FixedZone("", 3*3600)) }

	info := decode[map[string]any](t, get(t, s, "/rest/api/2/serverInfo"))

	if info["deploymentType"] != "DataCenter" || info["version"] != "11.2.7" ||
		info["buildNumber"] != float64(11020007) || info["serverTime"] != "2027-01-02T03:04:05.006+0300" {
		t.Errorf("serverInfo = %v", info)
	}
}

func TestDefaultHeaders(t *testing.T) {
	s := jiramock.New(t)

	authed := get(t, s, "/rest/api/2/serverInfo")
	anon := getAs(t, s, "/rest/api/2/serverInfo", "")

	for _, r := range []response{authed, anon} {
		if ct := r.header.Get("Content-Type"); ct != "application/json;charset=UTF-8" {
			t.Errorf("Content-Type = %q", ct)
		}
	}
	if u := authed.header.Get("X-AUSERNAME"); u != "svc-jira-cache" {
		t.Errorf("authenticated X-AUSERNAME = %q", u)
	}
	if u := anon.header.Get("X-AUSERNAME"); u != "anonymous" {
		t.Errorf("anonymous X-AUSERNAME = %q", u)
	}
}

func TestOmitUserHeader(t *testing.T) {
	s := jiramock.New(t)
	s.OmitUserHeader = true

	r := get(t, s, "/rest/api/2/myself")

	if _, ok := r.header["X-Ausername"]; ok {
		t.Errorf("X-AUSERNAME = %q", r.header.Get("X-AUSERNAME"))
	}
}

func TestMyself(t *testing.T) {
	s := jiramock.New(t)
	s.User = "svc bot"

	get(t, s, "/rest/api/2/myself").want(t, http.StatusOK, `{"active":true,"displayName":"svc bot","key":"JIRAUSER10500",`+
		`"name":"svc bot","self":"`+s.URL+`/rest/api/2/user?username=svc+bot","timeZone":"Etc/UTC"}`)
	getAs(t, s, "/rest/api/2/myself", "").want(t, http.StatusUnauthorized, notAuthenticated)
}

func TestOnlyTheExactBearerTokenAuthenticates(t *testing.T) {
	s := jiramock.New(t)
	s.Token = "secret"

	for _, auth := range []string{"", "secret", "Bearer secretx", "Bearer test-token", "bearer secret", "Basic secret"} {
		r := getAs(t, s, "/rest/api/2/myself", auth)

		if r.status != http.StatusUnauthorized || r.header.Get("X-AUSERNAME") != "anonymous" {
			t.Errorf("%q: %d as %q", auth, r.status, r.header.Get("X-AUSERNAME"))
		}
	}
	if r := getAs(t, s, "/rest/api/2/myself", "Bearer secret"); r.status != http.StatusOK {
		t.Errorf("Bearer secret: %d", r.status)
	}
}

func TestFields(t *testing.T) {
	s := jiramock.New(t)

	got := decode[[]map[string]any](t, get(t, s, "/rest/api/2/field"))

	if !reflect.DeepEqual(got, jiramock.ExampleFields()) {
		t.Errorf("fields = %v", got)
	}
	getAs(t, s, "/rest/api/2/field", "").want(t, http.StatusUnauthorized, notAuthenticated)

	s.Fields = []map[string]any{{"id": "x"}}
	get(t, s, "/rest/api/2/field").want(t, http.StatusOK, `[{"id":"x"}]`)
}

func TestUnknownPathIs404(t *testing.T) {
	s := jiramock.New(t)

	if r := get(t, s, "/rest/api/2/nope"); r.status != http.StatusNotFound {
		t.Errorf("status = %d", r.status)
	}
}

func TestSearchWindowIncludesItsEdges(t *testing.T) {
	s := jiramock.New(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	day, ms := 24*time.Hour, time.Millisecond
	plus2 := time.FixedZone("", 2*3600)
	s.Put(
		resolved(jiramock.NewIssue("PLAT-10", 1, now.Add(-7*day).In(plus2))),
		resolved(jiramock.NewIssue("PLAT-11", 2, now.Add(-7*day-ms))),
		jiramock.NewIssue("PLAT-2", 3, now.Add(-90*day).In(plus2)),
		jiramock.NewIssue("PLAT-3", 4, now.Add(-90*day-ms)),
		jiramock.NewIssue("PLAT-4", 5, now.Add(-7*day-ms)),
		jiramock.NewIssue("INFRA-1", 6, now),
	)

	got := search(t, s, windowJQL("PLAT", 7, 90)).keys()

	if want := []string{"PLAT-2", "PLAT-4", "PLAT-10"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestSampleSearchOrdersByUpdatedThenID(t *testing.T) {
	s := jiramock.New(t)
	now := jiramock.DefaultNow
	s.Put(
		jiramock.NewIssue("INFRA-1", 9, now.Add(-time.Hour)),
		jiramock.NewIssue("PLAT-1", 10, now.Add(-time.Hour)),
		jiramock.NewIssue("PLAT-2", 3, now),
		jiramock.NewIssue("PLAT-3", 4, now.AddDate(-2, 0, 0)),
		jiramock.NewIssue("OPS-1", 5, now),
	)

	got := search(t, s, `project in ("PLAT", "INFRA") ORDER BY updated DESC`).keys()

	if want := []string{"PLAT-2", "PLAT-1", "INFRA-1", "PLAT-3"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestSearchResolvesAliases(t *testing.T) {
	s := jiramock.New(t)
	s.Aliases = map[string]string{"OLD": "NEW"}
	s.Put(jiramock.NewIssue("NEW-1", 1, jiramock.DefaultNow))

	for _, jql := range []string{windowJQL("OLD", 30, 180), `project in ("OLD") ORDER BY updated DESC`} {
		if got := search(t, s, jql).keys(); !slices.Equal(got, []string{"NEW-1"}) {
			t.Errorf("%s: keys = %v", jql, got)
		}
	}
}

func TestSearchUnknownProject(t *testing.T) {
	s := jiramock.New(t)
	s.Projects = []string{"EMPTY"}
	s.Aliases = map[string]string{"GONE": "NOWHERE"}
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))

	get(t, s, searchPath(windowJQL("PLATX", 30, 180))).
		want(t, http.StatusBadRequest, errorBody("The value 'PLATX' does not exist for the field 'project'."))
	get(t, s, searchPath(`project in ("PLAT", "NOPE") ORDER BY updated DESC`)).
		want(t, http.StatusBadRequest, errorBody("The value 'NOPE' does not exist for the field 'project'."))
	get(t, s, searchPath(windowJQL("GONE", 30, 180))).
		want(t, http.StatusBadRequest, errorBody("The value 'GONE' does not exist for the field 'project'."))

	r := get(t, s, searchPath(windowJQL("EMPTY", 30, 180)))
	r.want(t, http.StatusOK, `{"expand":"schema,names","issues":[],"maxResults":50,"startAt":0,"total":0}`)
}

func TestSearchRejectsOtherJQL(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))

	for _, jql := range []string{
		"",
		"project = PLAT ORDER BY key",
		windowJQL("PLAT", 30, 180) + " ",
		" " + windowJQL("PLAT", 30, 180),
		`project in ("PLAT") ORDER BY updated DESC `,
		`project in ("PLAT","INFRA") ORDER BY updated DESC`,
	} {
		get(t, s, searchPath(jql)).
			want(t, http.StatusBadRequest, errorBody("Error in the JQL Query: unsupported by jiramock: "+jql))
	}
}

func TestSearchEnvelope(t *testing.T) {
	s := jiramock.New(t)
	iss := jiramock.NewIssue("PLAT-1", 77, jiramock.DefaultNow)
	s.Put(iss)

	r := get(t, s, searchPath(windowJQL("PLAT", 30, 180), "fields", "summary"))

	r.want(t, http.StatusOK, `{"expand":"schema,names","issues":[{"expand":"renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations",`+
		`"id":"77","self":"https://jira.example.com/rest/api/2/issue/77","key":"PLAT-1","fields":{"summary":"Issue PLAT-1"}}],`+
		`"maxResults":50,"startAt":0,"total":1}`)
}

func putMany(s *jiramock.Server, n int) {
	for i := 1; i <= n; i++ {
		s.Put(jiramock.NewIssue(fmt.Sprintf("PLAT-%d", i), i, jiramock.DefaultNow))
	}
}

func TestSearchPaging(t *testing.T) {
	s := jiramock.New(t)
	putMany(s, 51)
	jql := windowJQL("PLAT", 30, 180)

	for _, tc := range []struct {
		params             []string
		maxResults         int
		first, last, count int
	}{
		{nil, 50, 1, 50, 50},
		{[]string{"startAt", "0", "maxResults", "3"}, 3, 1, 3, 3},
		{[]string{"startAt", "1", "maxResults", "2"}, 2, 2, 3, 2},
		{[]string{"startAt", "50"}, 50, 51, 51, 1},
		{[]string{"startAt", "49", "maxResults", "1000"}, 1000, 50, 51, 2},
		{[]string{"maxResults", "0"}, 0, 0, 0, 0},
		{[]string{"startAt", "51"}, 50, 0, 0, 0},
		{[]string{"startAt", "52"}, 50, 0, 0, 0},
	} {
		p := search(t, s, jql, tc.params...)

		if p.Total != 51 || p.MaxResults != tc.maxResults || len(p.Issues) != tc.count {
			t.Errorf("%v: total %d, maxResults %d, %d issues", tc.params, p.Total, p.MaxResults, len(p.Issues))
			continue
		}
		if tc.count > 0 && (p.keys()[0] != fmt.Sprint("PLAT-", tc.first) || p.keys()[tc.count-1] != fmt.Sprint("PLAT-", tc.last)) {
			t.Errorf("%v: keys %v", tc.params, p.keys())
		}
	}
}

func TestSearchEchoesStartAt(t *testing.T) {
	s := jiramock.New(t)
	putMany(s, 3)

	if p := search(t, s, windowJQL("PLAT", 30, 180), "startAt", "2"); p.StartAt != 2 {
		t.Errorf("startAt = %d", p.StartAt)
	}
}

func TestSearchCapsMaxResults(t *testing.T) {
	s := jiramock.New(t)
	s.MaxResultsCap = 2
	putMany(s, 5)

	for requested, want := range map[string]int{"1": 1, "2": 2, "5": 2} {
		p := search(t, s, windowJQL("PLAT", 30, 180), "maxResults", requested)

		if p.MaxResults != want || len(p.Issues) != want {
			t.Errorf("maxResults=%s: echoed %d, %d issues", requested, p.MaxResults, len(p.Issues))
		}
	}
}

func TestSearchRejectsBadPaging(t *testing.T) {
	s := jiramock.New(t)
	jql := windowJQL("PLAT", 30, 180)

	for _, param := range []string{"startAt", "maxResults"} {
		for _, v := range []string{"-1", "x", ""} {
			get(t, s, searchPath(jql, param, v)).
				want(t, http.StatusBadRequest, errorBody("jiramock: "+param+" must be a non-negative integer"))
		}
	}
}

func TestSearchFieldsParameter(t *testing.T) {
	s := jiramock.New(t)
	iss := jiramock.ExampleIssue()
	s.Put(iss)
	jql := windowJQL("PLAT", 30, 180)

	for _, all := range [][]string{nil, {"fields", "*all"}, {"fields", "*navigable"}} {
		if got := search(t, s, jql, all...).Issues[0].Fields; !reflect.DeepEqual(got, iss.Fields) {
			t.Errorf("%v: fields = %v", all, got)
		}
	}
	got := search(t, s, jql, "fields", "id,key,updated,customfield_10002,bogus").Issues[0]
	want := map[string]any{"updated": iss.Fields["updated"], "customfield_10002": 3.0}
	if !reflect.DeepEqual(got.Fields, want) || got.ID != "10230" || got.Key != "PLAT-123" {
		t.Errorf("issue = %+v", got)
	}
}

func TestAnonymousSearch(t *testing.T) {
	jql := windowJQL("PLAT", 30, 180)
	for mode, want := range map[jiramock.AnonymousSearch]response{
		jiramock.AnonEmpty: {http.StatusOK, nil,
			`{"expand":"schema,names","issues":[],"maxResults":50,"startAt":0,"total":0}`},
		jiramock.AnonFieldHidden: {http.StatusBadRequest, nil,
			errorBody("Field 'issuetype' does not exist or this field cannot be viewed by anonymous users.")},
		jiramock.AnonNoProject: {http.StatusBadRequest, nil,
			errorBody("The value 'PLAT' does not exist for the field 'project'.")},
	} {
		s := jiramock.New(t)
		s.AnonymousSearch = mode
		s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))

		getAs(t, s, searchPath(jql), "Bearer wrong").want(t, want.status, want.body)
	}
}

func TestAnonymousSearchStillRejectsUnsupportedJQL(t *testing.T) {
	s := jiramock.New(t)

	getAs(t, s, searchPath("x"), "").
		want(t, http.StatusBadRequest, errorBody("Error in the JQL Query: unsupported by jiramock: x"))
}

func TestGetIssue(t *testing.T) {
	s := jiramock.New(t)
	iss := jiramock.ExampleIssue()
	s.Put(iss)

	get(t, s, "/rest/api/2/issue/PLAT-123").want(t, http.StatusOK, string(iss.JSON()))
	get(t, s, "/rest/api/2/issue/PLAT-123?fields=*all&expand=names").want(t, http.StatusOK, string(iss.JSON()))

	got := decode[struct{ Fields map[string]any }](t, get(t, s, "/rest/api/2/issue/PLAT-123?fields=summary,nope"))
	if want := map[string]any{"summary": iss.Fields["summary"]}; !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields = %v", got.Fields)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))
	notFound := errorBody("Issue Does Not Exist")

	get(t, s, "/rest/api/2/issue/PLAT-2").want(t, http.StatusNotFound, notFound)
	getAs(t, s, "/rest/api/2/issue/PLAT-1", "").want(t, http.StatusNotFound, notFound)
}

func TestGetIssueByOldKeyReturnsNewKey(t *testing.T) {
	s := jiramock.New(t)
	s.Aliases = map[string]string{"PLAT": "PLATFORM"}
	iss := jiramock.NewIssue("PLATFORM-5", 1, jiramock.DefaultNow)
	s.Put(iss)

	get(t, s, "/rest/api/2/issue/PLAT-5").want(t, http.StatusOK, string(iss.JSON()))
}

func TestPutReplacesAndDeleteRemoves(t *testing.T) {
	s := jiramock.New(t)
	a := jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow)
	b := jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow)
	b.Fields["summary"] = "replaced"
	s.Put(a, jiramock.NewIssue("PLAT-2", 2, jiramock.DefaultNow), jiramock.NewIssue("PLAT-3", 3, jiramock.DefaultNow))

	s.Put(b)
	s.Delete("PLAT-2", "PLAT-3")

	get(t, s, "/rest/api/2/issue/PLAT-1").want(t, http.StatusOK, string(b.JSON()))
	if p := search(t, s, windowJQL("PLAT", 30, 180)); p.Total != 1 {
		t.Errorf("total = %d", p.Total)
	}
}

func TestPutPanicsOnBadTimes(t *testing.T) {
	s := jiramock.New(t)
	badUpdated := jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow)
	badUpdated.Fields["updated"] = "2026-10-20T09:15:40Z"
	badComment := jiramock.NewIssue("PLAT-2", 2, jiramock.DefaultNow)
	badComment.Comments = []map[string]any{{"id": "1", "created": "yesterday", "body": ""}}

	for _, iss := range []jiramock.Issue{badUpdated, badComment} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", iss.Key)
				}
			}()
			s.Put(iss)
		}()
	}
}

type commentPage struct {
	StartAt, MaxResults, Total int
	Comments                   []map[string]any
}

func (p commentPage) ids() []string {
	ids := []string{}
	for _, c := range p.Comments {
		ids = append(ids, c["id"].(string))
	}
	return ids
}

func commentFixture(t *testing.T) *jiramock.Server {
	t.Helper()
	s := jiramock.New(t)
	at := func(id string, ts time.Time) map[string]any {
		return map[string]any{"id": id, "created": ts.Format(jiramock.Layout), "body": "c" + id}
	}
	t0 := jiramock.DefaultNow.Add(-24 * time.Hour)
	iss := jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow)
	iss.Comments = []map[string]any{
		at("10", t0),
		at("12", t0.Add(time.Hour)),
		at("9", t0.In(time.FixedZone("", -5*3600))),
		at("11", t0.Add(-time.Hour).In(time.FixedZone("", 2*3600))),
	}
	s.Put(iss)
	return s
}

func comments(t *testing.T, s *jiramock.Server, query string) commentPage {
	t.Helper()
	r := get(t, s, "/rest/api/2/issue/PLAT-1/comment"+query)
	if r.status != http.StatusOK {
		t.Fatalf("%s: %d %s", query, r.status, r.body)
	}
	return decode[commentPage](t, r)
}

func TestCommentsOrder(t *testing.T) {
	s := commentFixture(t)

	ascending, descending := []string{"11", "9", "10", "12"}, []string{"12", "10", "9", "11"}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"?orderBy=-created", descending},
		{"", ascending},
		{"?orderBy=created", ascending},
	} {
		if got := comments(t, s, tc.query).ids(); !slices.Equal(got, tc.want) {
			t.Errorf("%q: ids = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestCommentsPaging(t *testing.T) {
	s := commentFixture(t)

	p := comments(t, s, "?startAt=1&maxResults=2")
	if p.StartAt != 1 || p.MaxResults != 2 || p.Total != 4 || !slices.Equal(p.ids(), []string{"9", "10"}) {
		t.Errorf("page = %+v", p)
	}
	p = comments(t, s, "")
	if p.StartAt != 0 || p.MaxResults != 4 || len(p.Comments) != 4 {
		t.Errorf("all = %+v", p)
	}
	s.MaxResultsCap = 3
	p = comments(t, s, "")
	if p.MaxResults != 3 || !slices.Equal(p.ids(), []string{"11", "9", "10"}) {
		t.Errorf("capped = %+v", p)
	}
	get(t, s, "/rest/api/2/issue/PLAT-1/comment?maxResults=x").
		want(t, http.StatusBadRequest, errorBody("jiramock: maxResults must be a non-negative integer"))
	get(t, s, "/rest/api/2/issue/PLAT-1/comment?startAt=-1").
		want(t, http.StatusBadRequest, errorBody("jiramock: startAt must be a non-negative integer"))
}

func TestCommentsBody(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))
	iss := jiramock.ExampleIssue()
	s.Put(iss)

	get(t, s, "/rest/api/2/issue/PLAT-1/comment").
		want(t, http.StatusOK, `{"comments":[],"maxResults":0,"startAt":0,"total":0}`)
	got := decode[commentPage](t, get(t, s, "/rest/api/2/issue/PLAT-123/comment"))
	if !reflect.DeepEqual(got.Comments, iss.Comments) {
		t.Errorf("comments = %v", got.Comments)
	}
}

func TestCommentsNotFound(t *testing.T) {
	s := jiramock.New(t)
	s.Aliases = map[string]string{"OLD": "PLAT"}
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))
	notFound := errorBody("Issue Does Not Exist")

	get(t, s, "/rest/api/2/issue/PLAT-2/comment").want(t, http.StatusNotFound, notFound)
	getAs(t, s, "/rest/api/2/issue/PLAT-1/comment", "").want(t, http.StatusNotFound, notFound)
	if r := get(t, s, "/rest/api/2/issue/OLD-1/comment"); r.status != http.StatusOK {
		t.Errorf("alias: %d %s", r.status, r.body)
	}
}

func TestFaultTooManyRequests(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Path: "/rest/api/2/myself", Status: http.StatusTooManyRequests,
		Header: http.Header{"Retry-After": {"5"}}, Times: 1})

	r := get(t, s, "/rest/api/2/myself")

	r.want(t, http.StatusTooManyRequests, "{}")
	for k, v := range map[string]string{
		"Retry-After":                  "5",
		"X-RateLimit-Limit":            "100",
		"X-RateLimit-Remaining":        "0",
		"X-RateLimit-Interval-Seconds": "1",
		"X-RateLimit-FillRate":         "10",
		"Content-Type":                 "application/json;charset=UTF-8",
		"X-AUSERNAME":                  "svc-jira-cache",
	} {
		if got := r.header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if r := get(t, s, "/rest/api/2/myself"); r.status != http.StatusOK {
		t.Errorf("after fault: %d", r.status)
	}
}

func statuses(t *testing.T, s *jiramock.Server, path string, n int) []int {
	t.Helper()
	var got []int
	for range n {
		got = append(got, get(t, s, path).status)
	}
	return got
}

func TestFaultTimes(t *testing.T) {
	for times, want := range map[int][]int{
		2:  {500, 500, 200, 200},
		0:  {500, 500, 500, 500},
		-1: {500, 500, 500, 500},
	} {
		s := jiramock.New(t)
		s.Inject(jiramock.Fault{Status: http.StatusInternalServerError, Times: times})

		if got := statuses(t, s, "/rest/api/2/serverInfo", 4); !slices.Equal(got, want) {
			t.Errorf("Times %d: %v, want %v", times, got, want)
		}
	}
}

func TestFaultsMatchByPathPrefixInInjectionOrder(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Path: "/rest/api/2/issue/", Status: http.StatusBadGateway, Times: 1})
	s.Inject(jiramock.Fault{Path: "/rest/api/2/issue/PLAT-1", Status: http.StatusServiceUnavailable, Times: 1})
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))

	if r := get(t, s, "/rest/api/2/myself"); r.status != http.StatusOK {
		t.Errorf("unmatched path: %d", r.status)
	}
	got := statuses(t, s, "/rest/api/2/issue/PLAT-1", 3)
	if want := []int{502, 503, 200}; !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
}

func TestFaultDefaultBodies(t *testing.T) {
	for status, body := range map[int]string{
		500: "<html><body>Internal Server Error</body></html>",
		503: "<html><body>Internal Server Error</body></html>",
		200: "<html><body>Log in</body></html>",
		499: "<html><body>Log in</body></html>",
	} {
		s := jiramock.New(t)
		s.Inject(jiramock.Fault{Status: status})

		r := getAs(t, s, "/rest/api/2/myself", "")

		r.want(t, status, body)
		if ct := r.header.Get("Content-Type"); ct != "text/html;charset=UTF-8" {
			t.Errorf("%d: Content-Type = %q", status, ct)
		}
		if u := r.header.Get("X-AUSERNAME"); u != "anonymous" {
			t.Errorf("%d: X-AUSERNAME = %q", status, u)
		}
	}
}

func TestFaultOverrides(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Status: http.StatusInternalServerError, Body: `{"oops":1}`,
		Header: http.Header{"content-type": {"application/json"}, "X-Ausername": {"proxy"}}})

	r := get(t, s, "/rest/api/2/myself")

	r.want(t, http.StatusInternalServerError, `{"oops":1}`)
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if u := r.header.Get("X-AUSERNAME"); u != "proxy" {
		t.Errorf("X-AUSERNAME = %q", u)
	}
}

func TestFaultRedirectToLoginPage(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Path: "/rest/", Status: http.StatusFound, Header: http.Header{"Location": {"/login.jsp"}}})
	s.Inject(jiramock.Fault{Path: "/login.jsp", Status: http.StatusOK})
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	r := do(t, noFollow, s, "/rest/api/2/myself", "")
	if r.status != http.StatusFound || r.header.Get("Location") != "/login.jsp" {
		t.Errorf("got %d to %q", r.status, r.header.Get("Location"))
	}

	get(t, s, "/rest/api/2/myself").want(t, http.StatusOK, "<html><body>Log in</body></html>")
}

func TestRequestsRecordsEveryRequestInOrder(t *testing.T) {
	s := jiramock.New(t)
	s.Inject(jiramock.Fault{Path: "/rest/api/2/field", Status: http.StatusServiceUnavailable})

	get(t, s, "/rest/api/2/myself?a=1&a=2")
	getAs(t, s, "/rest/api/2/field", "")
	get(t, s, "/nope")

	got := s.Requests()
	if len(got) != 3 {
		t.Fatalf("requests = %v", got)
	}
	first := got[0]
	if first.Method != http.MethodGet || first.Path != "/rest/api/2/myself" ||
		!slices.Equal(first.Query["a"], []string{"1", "2"}) || first.Header.Get("Authorization") != "Bearer test-token" {
		t.Errorf("first = %+v", first)
	}
	if got[1].Path != "/rest/api/2/field" || got[1].Header.Get("Authorization") != "" || got[2].Path != "/nope" {
		t.Errorf("requests = %+v", got)
	}

	got[0].Path = "changed"
	if s.Requests()[0].Path != "/rest/api/2/myself" {
		t.Error("Requests returned shared storage")
	}
}

func TestOnRequestRunsBeforeEvaluationWithoutTheLock(t *testing.T) {
	s := jiramock.New(t)
	var seen []string
	s.OnRequest = func(r jiramock.Request) {
		seen = append(seen, r.Query.Get("startAt"))
		s.Put(jiramock.NewIssue(fmt.Sprint("PLAT-", len(s.Requests())), 1, jiramock.DefaultNow))
	}
	jql := windowJQL("PLAT", 30, 180)

	first := search(t, s, jql, "startAt", "0")
	second := search(t, s, jql, "startAt", "1")

	if first.Total != 1 || second.Total != 2 || !slices.Equal(seen, []string{"0", "1"}) {
		t.Errorf("totals %d, %d; seen %v", first.Total, second.Total, seen)
	}
}

func TestSearchWindowAcceptsHugeDayCounts(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow.AddDate(-1, 0, 0)))

	if p := search(t, s, windowJQL("PLAT", 1, 1_000_000)); p.Total != 1 {
		t.Errorf("total = %d", p.Total)
	}
}

func TestSearchWindowCountsDaysAs24Hours(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	s := jiramock.New(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, ny) // 2 days after DST ends
	s.Now = func() time.Time { return now }
	s.Put(jiramock.NewIssue("PLAT-1", 1, now.Add(-7*24*time.Hour-time.Millisecond)))

	if p := search(t, s, windowJQL("PLAT", 7, 7)); p.Total != 0 {
		t.Errorf("total = %d", p.Total)
	}
}

func TestPagingPastTheEndWithHugeStartAt(t *testing.T) {
	s := commentFixture(t)
	huge := strconv.Itoa(math.MaxInt)

	if p := search(t, s, windowJQL("PLAT", 30, 180), "startAt", huge); p.Total != 1 || len(p.Issues) != 0 {
		t.Errorf("search: %+v", p)
	}
	if p := comments(t, s, "?startAt="+huge); p.Total != 4 || len(p.Comments) != 0 {
		t.Errorf("comments: %+v", p)
	}
}

func TestProjectStaysKnownAfterItsLastIssueIsDeleted(t *testing.T) {
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow))
	s.Delete("PLAT-1")

	if p := search(t, s, windowJQL("PLAT", 30, 180)); p.Total != 0 {
		t.Errorf("total = %d", p.Total)
	}
}

func TestPutCopiesTheIssue(t *testing.T) {
	s := jiramock.New(t)
	iss := jiramock.ExampleIssue()
	want := string(iss.JSON())
	s.Put(iss)

	iss.Fields["updated"] = jiramock.DefaultNow.AddDate(-5, 0, 0).Format(jiramock.Layout)
	iss.Fields["status"].(map[string]any)["name"] = "changed"
	iss.Comments[0]["body"] = "changed"

	get(t, s, "/rest/api/2/issue/PLAT-123").want(t, http.StatusOK, want)
	got := decode[commentPage](t, get(t, s, "/rest/api/2/issue/PLAT-123/comment"))
	if !reflect.DeepEqual(got.Comments, jiramock.ExampleIssue().Comments) {
		t.Errorf("comments = %v", got.Comments)
	}
}

func TestConcurrentRequests(t *testing.T) {
	const n, failures = 40, 20
	s := jiramock.New(t)
	s.Put(jiramock.NewIssue("PLAT-1000", 1000, jiramock.DefaultNow))
	s.Inject(jiramock.Fault{Path: "/rest/api/2/search", Status: http.StatusServiceUnavailable, Times: failures})
	s.OnRequest = func(r jiramock.Request) {
		key := "PLAT-" + r.Query.Get("startAt")
		s.Put(jiramock.NewIssue(key, 1, jiramock.DefaultNow))
		s.Delete(key)
	}
	statuses := make(chan int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				s.URL+searchPath(windowJQL("PLAT", 30, 180), "startAt", strconv.Itoa(i+1)), nil)
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("Authorization", "Bearer "+s.Token)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_ = res.Body.Close()
			statuses <- res.StatusCode
		}()
	}
	go func() {
		wg.Wait()
		close(statuses)
	}()
	count := map[int]int{}
	for st := range statuses {
		count[st]++
		s.Requests()
		s.Inject(jiramock.Fault{Path: "/elsewhere", Status: http.StatusTeapot})
	}

	if want := map[int]int{http.StatusServiceUnavailable: failures, http.StatusOK: n - failures}; !reflect.DeepEqual(count, want) {
		t.Errorf("statuses = %v, want %v", count, want)
	}
}
