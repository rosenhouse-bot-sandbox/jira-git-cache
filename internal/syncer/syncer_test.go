package syncer_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/syncer"
)

var (
	t0      = jiramock.DefaultNow
	updated = t0.Add(-time.Hour)
	later   = t0.Add(-time.Minute)
)

type fixture struct {
	t     *testing.T
	jira  *jiramock.Server
	cfg   config.Config
	token string
	seen  int // requests before the current run
}

func newFixture(t *testing.T, projects ...string) *fixture {
	s := jiramock.New(t)
	s.Projects = projects
	cfg := config.Default()
	cfg.JiraURL = s.URL
	cfg.Projects = projects
	cfg.Fields = config.Fields{{ID: "customfield_10002", Name: "story_points"}}
	return &fixture{t: t, jira: s, cfg: cfg, token: s.Token}
}

func keyNum(key string) int {
	n, _ := strconv.Atoi(key[strings.Index(key, "-")+1:])
	return n
}

func (f *fixture) put(at time.Time, keys ...string) {
	for _, k := range keys {
		f.jira.Put(jiramock.NewIssue(k, 10000+keyNum(k), at))
	}
}

// run syncs with opts against the mock. It fills in Config, Now and ToolVersion when they are unset.
func (f *fixture) run(opts syncer.Options) (syncer.Result, error) {
	if opts.Config.JiraURL == "" {
		opts.Config = f.cfg
	}
	opts.Jira = &jira.Client{BaseURL: f.jira.URL, Token: f.token, Sleep: func(context.Context, time.Duration) error { return nil }}
	if opts.Now.IsZero() {
		opts.Now = t0
	}
	if opts.ToolVersion == "" {
		opts.ToolVersion = "v0.1.0"
	}
	f.seen = len(f.jira.Requests())
	return syncer.Run(f.t.Context(), opts)
}

func (f *fixture) mustRun(opts syncer.Options) syncer.Result {
	f.t.Helper()
	r, err := f.run(opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

// requests returns the last run's requests as sorted "path?query" lines.
func (f *fixture) requests() []string {
	var out []string
	for _, r := range f.jira.Requests()[f.seen:] {
		out = append(out, r.Path+"?"+r.Query.Encode())
	}
	slices.Sort(out)
	return out
}

func (f *fixture) issueGETs() []string {
	var out []string
	for _, r := range f.jira.Requests()[f.seen:] {
		if key, ok := strings.CutPrefix(r.Path, "/rest/api/2/issue/"); ok && !strings.HasSuffix(key, "/comment") {
			out = append(out, key)
		}
	}
	return out
}

// wantRequests lists the sorted requests of a run that fetches keys.
func (f *fixture) wantRequests(keys ...string) []string {
	issueFields := strings.Join(append(slices.Clone(snapshot.SystemFields), f.cfg.FieldIDs()...), ",")
	out := []string{"/rest/api/2/myself?", "/rest/api/2/field?"}
	for _, p := range f.cfg.Projects {
		q := url.Values{"jql": {f.cfg.WindowJQL(p)}, "fields": {"updated"}, "startAt": {"0"}, "maxResults": {"1000"}}
		out = append(out, "/rest/api/2/search?"+q.Encode())
	}
	for _, k := range keys {
		out = append(out,
			"/rest/api/2/issue/"+k+"?"+url.Values{"fields": {issueFields}}.Encode(),
			"/rest/api/2/issue/"+k+"/comment?maxResults=1000&orderBy=created&startAt=0")
	}
	slices.Sort(out)
	return out
}

func sortedKeys(s snapshot.Snapshot) []string {
	return slices.SortedFunc(maps.Keys(s.Issues), func(a, b string) int {
		if snapshot.LessKey(a, b) {
			return -1
		}
		return 1
	})
}

func TestRunDiffsAgainstPreviousSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after map[string]time.Time
		want          syncer.ProjectStats
		fetched       []string
	}{
		{
			name:    "new",
			after:   map[string]time.Time{"PLAT-1": updated},
			want:    syncer.ProjectStats{Project: "PLAT", InWindow: 1, New: 1},
			fetched: []string{"PLAT-1"},
		},
		{
			name:   "unchanged",
			before: map[string]time.Time{"PLAT-1": updated},
			after:  map[string]time.Time{"PLAT-1": updated},
			want:   syncer.ProjectStats{Project: "PLAT", InWindow: 1, Reused: 1},
		},
		{
			name:    "changed",
			before:  map[string]time.Time{"PLAT-1": updated},
			after:   map[string]time.Time{"PLAT-1": later},
			want:    syncer.ProjectStats{Project: "PLAT", InWindow: 1, Changed: 1},
			fetched: []string{"PLAT-1"},
		},
		{
			name:   "removed",
			before: map[string]time.Time{"PLAT-1": updated, "PLAT-2": updated},
			after:  map[string]time.Time{"PLAT-1": updated},
			want:   syncer.ProjectStats{Project: "PLAT", InWindow: 1, Reused: 1, Removed: 1},
		},
		{
			name:    "mixed",
			before:  map[string]time.Time{"PLAT-1": updated, "PLAT-2": updated, "PLAT-3": updated, "PLAT-4": updated},
			after:   map[string]time.Time{"PLAT-1": updated, "PLAT-2": later, "PLAT-4": updated, "PLAT-5": later},
			want:    syncer.ProjectStats{Project: "PLAT", InWindow: 4, New: 1, Changed: 1, Reused: 2, Removed: 1},
			fetched: []string{"PLAT-2", "PLAT-5"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "PLAT")
			for k, at := range tc.before {
				f.put(at, k)
			}
			prev := f.mustRun(syncer.Options{}).Snapshot
			f.jira.Delete(slices.Collect(maps.Keys(tc.before))...)
			for k, at := range tc.after {
				f.put(at, k)
			}

			r := f.mustRun(syncer.Options{Previous: prev, Now: t0.Add(time.Hour)})

			if want := []syncer.ProjectStats{tc.want}; !reflect.DeepEqual(r.Projects, want) {
				t.Errorf("Projects = %+v, want %+v", r.Projects, want)
			}
			if r.Fetched != tc.want.New+tc.want.Changed || r.Reused != tc.want.Reused || r.Removed != tc.want.Removed {
				t.Errorf("fetched, reused, removed = %d, %d, %d", r.Fetched, r.Reused, r.Removed)
			}
			if got, want := f.requests(), f.wantRequests(tc.fetched...); !slices.Equal(got, want) {
				t.Errorf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if got, want := sortedKeys(r.Snapshot), slices.Sorted(maps.Keys(tc.after)); !slices.Equal(got, want) {
				t.Errorf("stored %v, want %v", got, want)
			}
			for k, iss := range r.Snapshot.Issues {
				if !slices.Contains(tc.fetched, k) && !reflect.DeepEqual(iss, prev.Issues[k]) {
					t.Errorf("%s was not copied from the previous snapshot", k)
				}
			}
			if r.FullRefreshReason != "" {
				t.Errorf("FullRefreshReason = %q", r.FullRefreshReason)
			}
		})
	}
}

func TestRunCountsRemovedIssuesOfOtherProjectsOnlyInTotal(t *testing.T) {
	f := newFixture(t, "PLAT")
	prev := f.mustRun(syncer.Options{}).Snapshot
	prev.Issues["GONE-1"] = snapshot.Issue{Key: "GONE-1"}

	r := f.mustRun(syncer.Options{Previous: prev})

	if r.Removed != 1 || r.Projects[0].Removed != 0 {
		t.Errorf("removed = %d, PLAT removed = %d; want 1, 0", r.Removed, r.Projects[0].Removed)
	}
}

func TestRunStoresFetchedIssuesAndMeta(t *testing.T) {
	f := newFixture(t, "PLAT", "INFRA")
	f.put(updated, "PLAT-1", "PLAT-2", "INFRA-7")
	now := time.Date(2026, 10, 22, 9, 0, 41, 999_000_000, time.FixedZone("+0200", 2*60*60))

	r := f.mustRun(syncer.Options{Now: now, ToolVersion: "v1.2.3"})

	fetchedAt := time.Date(2026, 10, 22, 7, 0, 41, 0, time.UTC)
	want := snapshot.Meta{
		FetchedAt:  &fetchedAt,
		LastFullAt: &fetchedAt,
		JiraURL:    f.cfg.JiraURL,
		Projects: map[string]snapshot.ProjectMeta{
			"PLAT":  {JQL: f.cfg.WindowJQL("PLAT"), Count: 2},
			"INFRA": {JQL: f.cfg.WindowJQL("INFRA"), Count: 1},
		},
		SchemaVersion: snapshot.SchemaVersion,
		ToolVersion:   "v1.2.3",
		FieldsHash:    f.cfg.FieldsHash(),
	}
	if !reflect.DeepEqual(r.Snapshot.Meta, want) {
		t.Errorf("Meta = %+v\nwant %+v", r.Snapshot.Meta, want)
	}
	if r.Snapshot.Meta.FetchedAt.Location() != time.UTC {
		t.Errorf("FetchedAt is in %v, want UTC", r.Snapshot.Meta.FetchedAt.Location())
	}
	if len(r.Snapshot.Fields) != 6 || r.Snapshot.Fields[0] != (snapshot.Field{ID: "customfield_10002", Name: "Story Points"}) {
		t.Errorf("Fields = %v", r.Snapshot.Fields)
	}
	iss := r.Snapshot.Issues["INFRA-7"]
	if iss.Summary != "Issue INFRA-7" || !reflect.DeepEqual(iss.Custom, map[string]any{"story_points": nil}) {
		t.Errorf("INFRA-7 = %+v", iss)
	}
	wantStats := []syncer.ProjectStats{{Project: "PLAT", InWindow: 2, New: 2}, {Project: "INFRA", InWindow: 1, New: 1}}
	if !reflect.DeepEqual(r.Projects, wantStats) {
		t.Errorf("Projects = %+v", r.Projects)
	}
	if got, want := r.CommitMessage(), "sync: PLAT=2 INFRA=1 fetched=3 reused=0 removed=0"; got != want {
		t.Errorf("CommitMessage = %q, want %q", got, want)
	}
}

func TestRunFetchesInKeyOrder(t *testing.T) {
	f := newFixture(t, "PLAT", "INFRA")
	f.cfg.Concurrency = 1
	f.put(updated, "PLAT-10", "PLAT-2", "INFRA-1")

	f.mustRun(syncer.Options{})

	if got, want := f.issueGETs(), []string{"INFRA-1", "PLAT-2", "PLAT-10"}; !slices.Equal(got, want) {
		t.Errorf("fetched %v, want %v", got, want)
	}
}

func TestRunHonorsConcurrency(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.cfg.Concurrency = 2
	f.put(updated, "PLAT-1", "PLAT-2", "PLAT-3", "PLAT-4", "PLAT-5")
	var mu sync.Mutex
	inFlight, peak := 0, 0
	full := make(chan struct{})
	var fill sync.Once
	f.jira.OnRequest = func(r jiramock.Request) {
		if !strings.HasPrefix(r.Path, "/rest/api/2/issue/") || strings.HasSuffix(r.Path, "/comment") {
			return
		}
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		if inFlight == 2 {
			fill.Do(func() { close(full) })
		}
		mu.Unlock()
		select {
		case <-full:
		case <-time.After(time.Second):
		}
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
	}

	f.mustRun(syncer.Options{})

	if peak != 2 {
		t.Errorf("peak in flight = %d, want 2", peak)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.cfg.Fields = config.Fields{{ID: "customfield_10100", Name: "sprint"}, {ID: "customfield_10002", Name: "story_points"}}
	f.jira.Put(jiramock.ExampleIssue())
	f.put(updated, "PLAT-1", "PLAT-2", "PLAT-3")

	a := f.mustRun(syncer.Options{Full: true}).Snapshot.Encode()
	b := f.mustRun(syncer.Options{Full: true}).Snapshot.Encode()

	if !reflect.DeepEqual(a, b) {
		t.Error("two runs encoded different bytes")
	}
	if len(a) != 6 {
		t.Errorf("encoded %d files, want 6", len(a))
	}
}

func TestRunIgnoresTimeZoneOfUpdated(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated.In(time.FixedZone("+0200", 2*60*60)), "PLAT-1", "PLAT-2")
	prev := f.mustRun(syncer.Options{}).Snapshot
	f.put(updated.UTC(), "PLAT-1", "PLAT-2")

	r := f.mustRun(syncer.Options{Previous: prev})

	if r.Projects[0].Changed != 0 || r.Reused != 2 || len(f.issueGETs()) != 0 {
		t.Errorf("stats = %+v, issue GETs = %v", r.Projects[0], f.issueGETs())
	}
}

func TestRunFullRefreshTriggers(t *testing.T) {
	const age = 24*time.Hour - 5*time.Minute
	type trigger struct {
		name   string
		change func(*syncer.Options)
		want   string
	}
	byPriority := []trigger{
		{"full flag", func(o *syncer.Options) { o.Full = true }, "--full"},
		{"never full", func(o *syncer.Options) { o.Previous.Meta.LastFullAt = nil }, "no previous full refresh"},
		{"schema", func(o *syncer.Options) { o.Previous.Meta.SchemaVersion = 0 }, "schema_version changed"},
		{"fields", func(o *syncer.Options) { o.Config.Fields = o.Config.Fields[:0] }, "fields changed"},
		{"jira url", func(o *syncer.Options) { o.Previous.Meta.JiraURL = "https://old.example.com" }, "jira_url changed"},
		{"age at limit", func(o *syncer.Options) { o.Now = t0.Add(age) }, "last full refresh older than 24h"},
	}
	cases := []trigger{
		{"none", func(*syncer.Options) {}, ""},
		{"age below limit", func(o *syncer.Options) { o.Now = t0.Add(age - time.Millisecond) }, ""},
	}
	for i, tc := range byPriority {
		cases = append(cases, trigger{tc.name + " and later triggers", func(o *syncer.Options) {
			for _, later := range byPriority[i:] {
				later.change(o)
			}
		}, tc.want})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "PLAT")
			f.put(updated, "PLAT-1")
			opts := syncer.Options{Config: f.cfg, Previous: f.mustRun(syncer.Options{}).Snapshot, Now: t0.Add(time.Hour)}
			tc.change(&opts)

			r := f.mustRun(opts)

			if r.FullRefreshReason != tc.want {
				t.Errorf("FullRefreshReason = %q, want %q", r.FullRefreshReason, tc.want)
			}
			wantLastFull, wantChanged := t0, 0
			if tc.want != "" {
				wantLastFull, wantChanged = opts.Now.Truncate(time.Second), 1
			}
			if !r.Snapshot.Meta.LastFullAt.Equal(wantLastFull) {
				t.Errorf("LastFullAt = %v, want %v", r.Snapshot.Meta.LastFullAt, wantLastFull)
			}
			if r.Projects[0].Changed != wantChanged || r.Reused != 1-wantChanged {
				t.Errorf("stats = %+v", r.Projects[0])
			}
		})
	}
}

func TestRunAttributesIssuesToTheEnumeratingProject(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1")
	prev := f.mustRun(syncer.Options{}).Snapshot
	f.jira.Aliases = map[string]string{"PLAT": "PLATFORM"}
	f.jira.Delete("PLAT-1")
	f.put(updated, "PLATFORM-1", "PLATFORM-2")

	r := f.mustRun(syncer.Options{Previous: prev})

	if want := []syncer.ProjectStats{{Project: "PLAT", InWindow: 2, New: 2, Removed: 1}}; !reflect.DeepEqual(r.Projects, want) {
		t.Errorf("Projects = %+v", r.Projects)
	}
	if got, want := r.CommitMessage(), "sync: PLAT=2 fetched=2 reused=0 removed=1"; got != want {
		t.Errorf("CommitMessage = %q, want %q", got, want)
	}
	if got := sortedKeys(r.Snapshot); !slices.Equal(got, []string{"PLATFORM-1", "PLATFORM-2"}) {
		t.Errorf("stored %v", got)
	}
}

func TestRunKeepsTheFirstProjectOfADuplicateKey(t *testing.T) {
	f := newFixture(t, "PLAT", "OLD")
	f.jira.Aliases = map[string]string{"OLD": "PLAT"}
	f.put(updated, "PLAT-1")

	r := f.mustRun(syncer.Options{})

	want := []syncer.ProjectStats{{Project: "PLAT", InWindow: 1, New: 1}, {Project: "OLD"}}
	if !reflect.DeepEqual(r.Projects, want) {
		t.Errorf("Projects = %+v", r.Projects)
	}
	if got, want := r.CommitMessage(), "sync: PLAT=1 OLD=0 fetched=1 reused=0 removed=0"; got != want {
		t.Errorf("CommitMessage = %q, want %q", got, want)
	}
}

// shiftOnSecondPage adds an issue before each of the first n requests for a second page.
func shiftOnSecondPage(f *fixture, n int) {
	f.jira.MaxResultsCap = 2
	next := 100
	f.jira.OnRequest = func(r jiramock.Request) {
		if r.Query.Get("startAt") == "2" && n > 0 {
			n--
			next++
			f.put(updated, "PLAT-"+strconv.Itoa(next))
		}
	}
}

func TestRunReenumeratesWhenTotalShifts(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1", "PLAT-2", "PLAT-3")
	shiftOnSecondPage(f, 2)

	r := f.mustRun(syncer.Options{})

	if r.Projects[0].InWindow != 5 || r.Fetched != 5 {
		t.Errorf("stats = %+v, fetched %d", r.Projects[0], r.Fetched)
	}
}

func TestRunFailsWhenTotalKeepsShifting(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1", "PLAT-2", "PLAT-3")
	shiftOnSecondPage(f, 3)

	_, err := f.run(syncer.Options{})

	if !errors.Is(err, jira.ErrWindowShifted) {
		t.Errorf("err = %v, want ErrWindowShifted", err)
	}
}

func TestRunRetriesRateLimit(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1")
	f.jira.Inject(jiramock.Fault{Path: "/rest/api/2/issue/PLAT-1", Status: http.StatusTooManyRequests, Times: 1})

	r := f.mustRun(syncer.Options{})

	if r.Fetched != 1 || len(f.issueGETs()) != 2 {
		t.Errorf("fetched %d, issue GETs %v", r.Fetched, f.issueGETs())
	}
}

func TestRunFailsOnPersistentServerError(t *testing.T) {
	for _, path := range []string{"/rest/api/2/issue/PLAT-2", "/rest/api/2/issue/PLAT-2/comment"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t, "PLAT")
			f.put(updated, "PLAT-1", "PLAT-2")
			f.jira.Inject(jiramock.Fault{Path: path, Status: http.StatusServiceUnavailable})

			_, err := f.run(syncer.Options{})

			var apiErr *jira.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("err = %v, want 503", err)
			}
		})
	}
}

func TestRunSkipsIssuesThatLeaveDuringSync(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1", "PLAT-2", "PLAT-3", "PLAT-10")
	f.jira.Inject(jiramock.Fault{
		Path:   "/rest/api/2/issue/PLAT-10",
		Status: http.StatusOK,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   string(jiramock.NewIssue("MOVED-1", 10010, updated).JSON()),
		Times:  1,
	})
	f.jira.OnRequest = func(r jiramock.Request) {
		switch r.Path {
		case "/rest/api/2/issue/PLAT-2":
			time.Sleep(50 * time.Millisecond) // It finishes last, so only sorting puts it first.
			f.jira.Delete("PLAT-2")
		case "/rest/api/2/issue/PLAT-3/comment":
			f.jira.Delete("PLAT-3")
		}
	}

	r := f.mustRun(syncer.Options{})

	if want := []string{"PLAT-2", "PLAT-3", "PLAT-10"}; !slices.Equal(r.Skipped, want) {
		t.Errorf("Skipped = %v, want %v", r.Skipped, want)
	}
	if got := sortedKeys(r.Snapshot); !slices.Equal(got, []string{"PLAT-1"}) {
		t.Errorf("stored %v", got)
	}
	if got, want := r.CommitMessage(), "sync: PLAT=1 fetched=1 reused=0 removed=0"; got != want {
		t.Errorf("CommitMessage = %q, want %q", got, want)
	}
	if slices.Contains(f.requests(), "/rest/api/2/issue/PLAT-10/comment?maxResults=1000&orderBy=created&startAt=0") {
		t.Error("fetched comments of an issue whose key changed")
	}
}

func TestRunDropsChangedIssueThatLeavesDuringSync(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.put(updated, "PLAT-1", "PLAT-2")
	first := f.mustRun(syncer.Options{})
	f.put(later, "PLAT-1")
	f.jira.OnRequest = func(r jiramock.Request) {
		if r.Path == "/rest/api/2/issue/PLAT-1" {
			f.jira.Delete("PLAT-1")
		}
	}

	r := f.mustRun(syncer.Options{Previous: first.Snapshot})

	if got := sortedKeys(r.Snapshot); !slices.Equal(got, []string{"PLAT-2"}) {
		t.Errorf("stored %v", got)
	}
}

func TestRunStopsOnBadToken(t *testing.T) {
	f := newFixture(t, "PLAT")
	f.token = "wrong"

	_, err := f.run(syncer.Options{})

	if !errors.Is(err, jira.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
	if got := f.requests(); !slices.Equal(got, []string{"/rest/api/2/myself?"}) {
		t.Errorf("requests = %v", got)
	}
}

func TestRunFails(t *testing.T) {
	const searchPath = "/rest/api/2/search"
	searchBody := func(key, fields string) string {
		return `{"startAt":0,"maxResults":1000,"total":1,"issues":[{"id":"1","key":"` + key + `","fields":` + fields + `}]}`
	}
	type failure struct {
		name  string
		setup func(*fixture)
		want  string
	}
	cases := []failure{
		{"fields", func(f *fixture) {
			f.jira.Inject(jiramock.Fault{Path: "/rest/api/2/field", Status: http.StatusForbidden, Body: "{}"})
		}, "/rest/api/2/field: 403"},
		{"search", func(f *fixture) { f.cfg.Projects = []string{"NOPE"} }, "The value 'NOPE' does not exist"},
		{"missing updated", func(f *fixture) {
			f.jira.Inject(jiramock.Fault{Path: searchPath, Status: http.StatusOK, Body: searchBody("PLAT-1", `{}`)})
		}, "PLAT-1: updated"},
		{"bad updated", func(f *fixture) {
			f.jira.Inject(jiramock.Fault{Path: searchPath, Status: http.StatusOK, Body: searchBody("PLAT-1", `{"updated":"yesterday"}`)})
		}, "PLAT-1: updated"},
		{"bad issue", func(f *fixture) {
			iss := jiramock.NewIssue("PLAT-1", 10001, updated)
			delete(iss.Fields, "summary")
			f.jira.Put(iss)
		}, "PLAT-1: missing summary"},
	}
	for _, key := range []string{"plat-1", "PlAT-1", "xPLAT-1", "1PLAT-1", "PLAT-0", "PLAT-01", "PLAT-1x", "PLAT1"} {
		cases = append(cases, failure{"bad key " + key, func(f *fixture) {
			f.jira.Inject(jiramock.Fault{Path: searchPath, Status: http.StatusOK, Body: searchBody(key, `{"updated":"2026-10-22T06:00:00.000+0000"}`)})
		}, `invalid issue key "` + key + `"`})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "PLAT")
			tc.setup(f)

			_, err := f.run(syncer.Options{})

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
