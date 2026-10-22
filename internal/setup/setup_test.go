package setup_test

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/setup"
)

func TestMain(m *testing.M) {
	cleanup := gittest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

var now = time.Date(2026, 10, 22, 7, 30, 0, 0, time.UTC)

type fixture struct {
	jira   *jiramock.Server
	remote string
	dir    string
	out    *strings.Builder
	opts   setup.Options
}

// newFixture serves one PLAT issue with no custom fields to a fresh clone.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := jiramock.New(t)
	srv.Put(jiramock.NewIssue("PLAT-1", 10001, jiramock.DefaultNow.Add(-time.Hour)))
	remote := gittest.Remote(t)
	dir := gittest.Clone(t, remote)
	out := &strings.Builder{}
	return &fixture{jira: srv, remote: remote, dir: dir, out: out, opts: setup.Options{
		Dir:         dir,
		Stdin:       strings.NewReader(""),
		Stdout:      out,
		Token:       srv.Token,
		ToolVersion: "v0.1.0",
		Now:         now,
		ReadPassword: func(string) (string, error) {
			t.Error("unexpected ReadPassword")
			return "", nil
		},
		NonInteractive: true,
		Answers:        setup.Answers{JiraURL: ptr(srv.URL), Projects: ptr("PLAT")},
	}}
}

func (f *fixture) run(t *testing.T) error {
	t.Helper()
	return setup.Run(t.Context(), f.opts)
}

func (f *fixture) mustRun(t *testing.T) string {
	t.Helper()
	if err := f.run(t); err != nil {
		t.Fatalf("%v\noutput:\n%s", err, f.out)
	}
	return f.out.String()
}

func (f *fixture) file(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *fixture) write(t *testing.T, path, data string) {
	t.Helper()
	path = filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) remoteBranches(t *testing.T) string {
	t.Helper()
	return gittest.Run(t, f.remote, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
}

func ptr(s string) *string { return &s }

func nextSteps(branch string) string {
	return `Next steps:
  1. Commit and push main:
       git add .jira-git-cache .github && git commit -m "Configure jira-git-cache" && git push
  2. Create the environment "jira", limit it to the main branch, and store the token there:
       gh secret set JIRA_TOKEN --env jira
  3. Protect main.
  4. Let only GitHub Actions push ` + branch + `: add a ruleset that blocks updates,
     force pushes, and deletions, with GitHub Actions as the only bypass actor.
  5. Start the first sync:
       gh workflow run jira-sync.yaml
`
}

const issue2Transcript = `Jira URL: https://jira.example.com
✓ Authenticated as svc-jira-cache on Jira Data Center 10.3.4

Projects (comma-separated keys): PLAT, INFRA
Keep every issue updated within days [30]:
Also keep unresolved issues updated within days [180]:
✓ PLAT   1,204 issues in window
✓ INFRA    387 issues in window

Runner labels [self-hosted]: self-hosted, jira
Cache branch [dev/jira-cache]:

Custom fields set on 100 recent issues:
     id                 Jira name       set on
  1  customfield_10002  Story Points       91  *
  2  customfield_10100  Sprint             88  *
  3  customfield_10007  Epic Link          70  *
  4  customfield_10500  Team               52
  5  customfield_10811  Legacy Ticket#      3
Include which fields? [1,2,3]: 1-4

Wrote .jira-git-cache/config.yml
Wrote .jira-git-cache/.gitignore
Wrote .github/workflows/jira-sync.yaml

Create dev/jira-cache on origin with an empty snapshot? [Y/n]
✓ Pushed dev/jira-cache (never synced)

Next steps:
  1. Commit and push main:
       git add .jira-git-cache .github && git commit -m "Configure jira-git-cache" && git push
  2. Create the environment "jira", limit it to the main branch, and store the token there:
       gh secret set JIRA_TOKEN --env jira
  3. Protect main.
  4. Let only GitHub Actions push dev/jira-cache: add a ruleset that blocks updates,
     force pushes, and deletions, with GitHub Actions as the only bypass actor.
  5. Start the first sync:
       gh workflow run jira-sync.yaml
`

const issue2Config = `jira_url: https://jira.example.com
cache_branch: dev/jira-cache
projects: [PLAT, INFRA]
window:
  days: 30
  unresolved_days: 180
full_refresh_every: 24h
concurrency: 8
fields:
  customfield_10002: story_points
  customfield_10100: sprint
  customfield_10007: epic_link
  customfield_10500: team
`

// putIssue2Data stores 1,204 PLAT and 387 INFRA issues. The 100 most recent
// set custom fields as often as #2's table says, and unset them as "", [], null
// or the Parent Link placeholder.
func putIssue2Data(srv *jiramock.Server) {
	srv.Fields = append(jiramock.ExampleFields(), map[string]any{
		"id": "customfield_10811", "name": "Legacy Ticket#", "custom": true,
		"schema": map[string]any{"type": "string", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:textfield", "customId": 10811.0},
	})
	parentLink := jiramock.ExampleIssue().Fields["customfield_10700"]
	set := func(j, n int, v, unset any) any {
		if j < n {
			return v
		}
		return unset
	}
	var issues []jiramock.Issue
	for j := range 1204 + 387 {
		key := fmt.Sprint("PLAT-", j+1)
		if j >= 1204 {
			key = fmt.Sprint("INFRA-", j-1203)
		}
		iss := jiramock.NewIssue(key, 20000+j, jiramock.DefaultNow.Add(-time.Duration(j)*time.Minute))
		if j < 100 {
			iss.Fields["customfield_10002"] = set(j, 91, 3.0, nil)
			iss.Fields["customfield_10100"] = set(j, 88, []any{jiramock.Sprint8x(42, "Sprint 42", "ACTIVE", "")}, []any{})
			iss.Fields["customfield_10007"] = set(j, 70, "PLAT-90", nil)
			iss.Fields["customfield_10500"] = set(j, 52, map[string]any{"value": "Platform"}, nil)
			iss.Fields["customfield_10811"] = set(j, 3, "LEG-1", "")
			iss.Fields["customfield_10600"] = []any{}
			iss.Fields["customfield_10700"] = parentLink
		}
		issues = append(issues, iss)
	}
	srv.Put(issues...)
}

func TestRunReproducesIssue2(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Delete("PLAT-1")
	putIssue2Data(f.jira)
	f.opts.NonInteractive = false
	f.opts.Answers = setup.Answers{
		JiraURL:        ptr(f.jira.URL + "/"),
		Projects:       ptr("PLAT, INFRA"),
		Days:           ptr(""),
		UnresolvedDays: ptr(""),
		RunnerLabels:   ptr("self-hosted, jira"),
		CacheBranch:    ptr(""),
		Fields:         ptr("1-4"),
		CreateBranch:   ptr(""),
	}

	out := f.mustRun(t)

	withMock := func(s string) string { return strings.ReplaceAll(s, "https://jira.example.com", f.jira.URL) }
	want := strings.Replace(withMock(issue2Transcript), f.jira.URL, f.jira.URL+"/", 1)
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if got := f.file(t, ".jira-git-cache/config.yml"); got != withMock(issue2Config) {
		t.Errorf("config.yml:\n%s", got)
	}
	if got := f.file(t, ".jira-git-cache/.gitignore"); got != setup.Gitignore {
		t.Errorf(".gitignore:\n%s", got)
	}
	if got := f.file(t, ".github/workflows/jira-sync.yaml"); got != issue2Workflow {
		t.Errorf("workflow:\n%s", got)
	}
	cfg, err := config.Parse([]byte(issue2Config))
	if err != nil {
		t.Fatal(err)
	}
	if got := gittest.Run(t, f.remote, "ls-tree", "-r", "--name-only", "dev/jira-cache"); got != "_meta/snapshot.json" {
		t.Errorf("branch files: %s", got)
	}
	wantMeta := `{
  "fetched_at": null,
  "last_full_at": null,
  "jira_url": "` + f.jira.URL + `",
  "projects": {},
  "schema_version": 1,
  "tool_version": "v0.1.0",
  "fields_hash": "` + cfg.FieldsHash() + `"
}`
	if got := gittest.Run(t, f.remote, "show", "dev/jira-cache:_meta/snapshot.json"); got != wantMeta {
		t.Errorf("snapshot.json:\n%s", got)
	}
	if got := gittest.Run(t, f.remote, "log", "-1", "--format=%s %cI", "dev/jira-cache"); got != "setup: empty snapshot 2026-10-22T07:30:00+00:00" {
		t.Errorf("commit: %s", got)
	}
}

func TestRunNonInteractiveUsesDefaults(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Put(jiramock.ExampleIssue())

	out := f.mustRun(t)

	want := `Jira URL: ` + f.jira.URL + `
✓ Authenticated as svc-jira-cache on Jira Data Center 10.3.4

Projects (comma-separated keys): PLAT
✓ PLAT  2 issues in window

Custom fields set on 2 recent issues:
     id                 Jira name     set on
  1  customfield_10002  Story Points      1  *
  2  customfield_10007  Epic Link         1  *
  3  customfield_10100  Sprint            1  *
  4  customfield_10500  Team              1
  5  customfield_10600  Reviewers         1

Wrote .jira-git-cache/config.yml
Wrote .jira-git-cache/.gitignore
Wrote .github/workflows/jira-sync.yaml

✓ Pushed dev/jira-cache (never synced)

` + nextSteps("dev/jira-cache")
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	wantConfig := `jira_url: ` + f.jira.URL + `
cache_branch: dev/jira-cache
projects: [PLAT]
window:
  days: 30
  unresolved_days: 180
full_refresh_every: 24h
concurrency: 8
fields:
  customfield_10002: story_points
  customfield_10007: epic_link
  customfield_10100: sprint
`
	if got := f.file(t, ".jira-git-cache/config.yml"); got != wantConfig {
		t.Errorf("config.yml:\n%s", got)
	}
	if got := f.file(t, ".github/workflows/jira-sync.yaml"); !strings.Contains(got, "runs-on: [self-hosted]\n") {
		t.Errorf("workflow:\n%s", got)
	}
}

func TestRunInteractive(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Projects = []string{"INFRA"}
	f.opts.NonInteractive = false
	f.opts.Answers = setup.Answers{}
	f.opts.Token = ""
	var prompts []string
	f.opts.ReadPassword = func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		return " " + f.jira.Token + "\n", nil
	}
	f.opts.Stdin = strings.NewReader(strings.Join([]string{
		f.jira.URL + "/",
		"",
		"plat, 1x",
		"plat,infra",
		"x",
		"0",
		"60",
		"50",
		"  ",
		"a b",
		"ci, linux",
		"main",
		"cache/jira",
		"maybe",
		"N",
	}, "\r\n") + "\n")

	out := f.mustRun(t)

	want := `Jira URL: ✓ Authenticated as svc-jira-cache on Jira Data Center 10.3.4

Projects (comma-separated keys): config.yml: projects: invalid key ""
Projects (comma-separated keys): config.yml: projects: invalid key "1X"
Projects (comma-separated keys): Keep every issue updated within days [30]: "x" is not a whole number of days
Keep every issue updated within days [30]: config.yml: window.days: must be at least 1
Keep every issue updated within days [30]: Also keep unresolved issues updated within days [180]: config.yml: window.unresolved_days: must be at least window.days (60)
Also keep unresolved issues updated within days [180]: ✓ PLAT   1 issues in window
✓ INFRA  0 issues in window

Runner labels [self-hosted]: invalid runner label "a b"
Runner labels [self-hosted]: Cache branch [dev/jira-cache]: config.yml: cache_branch: must not be main
Cache branch [dev/jira-cache]: ` + "\n" + `No custom fields set on 1 recent issues.

Wrote .jira-git-cache/config.yml
Wrote .jira-git-cache/.gitignore
Wrote .github/workflows/jira-sync.yaml

Create cache/jira on origin with an empty snapshot? [Y/n] invalid answer "maybe": use y or n
Create cache/jira on origin with an empty snapshot? [Y/n] Skipped cache/jira

` + nextSteps("cache/jira")
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if len(prompts) != 1 || prompts[0] != "Jira token (hidden; or set JIRA_TOKEN): " {
		t.Errorf("ReadPassword prompts: %q", prompts)
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JiraURL != f.jira.URL || strings.Join(cfg.Projects, ",") != "PLAT,INFRA" || cfg.Window != (config.Window{Days: 60, UnresolvedDays: 180}) ||
		cfg.CacheBranch != "cache/jira" || cfg.Fields != nil {
		t.Errorf("config: %+v", cfg)
	}
	if got := f.file(t, ".github/workflows/jira-sync.yaml"); !strings.Contains(got, "runs-on: [ci, linux]\n") {
		t.Errorf("workflow:\n%s", got)
	}
	if got := f.remoteBranches(t); got != "main" {
		t.Errorf("remote branches: %s", got)
	}
}

func TestRunKeepsExistingFilesAndBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Projects = []string{"INFRA"}
	cfg := "jira_url: " + f.jira.URL + "\ncache_branch: cache/jira\nprojects: [INFRA, PLAT]\n"
	f.write(t, ".jira-git-cache/config.yml", cfg)
	f.write(t, ".github/workflows/jira-sync.yaml", "custom\n")
	gittest.Seed(t, f.remote, "cache/jira", map[string][]byte{"keep": []byte("me\n")})
	f.opts.Dir = filepath.Join(f.dir, ".github")
	f.opts.Answers = setup.Answers{}

	out := f.mustRun(t)

	want := `✓ Authenticated as svc-jira-cache on Jira Data Center 10.3.4

✓ INFRA  0 issues in window
✓ PLAT   1 issues in window

Kept .jira-git-cache/config.yml (already exists)
Wrote .jira-git-cache/.gitignore
Kept .github/workflows/jira-sync.yaml (already exists)

✓ cache/jira already exists on origin; left it alone

` + nextSteps("cache/jira")
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if got := f.file(t, ".jira-git-cache/config.yml"); got != cfg {
		t.Errorf("config.yml changed:\n%s", got)
	}
	if got := f.file(t, ".github/workflows/jira-sync.yaml"); got != "custom\n" {
		t.Errorf("workflow changed:\n%s", got)
	}
	if got := f.file(t, ".jira-git-cache/.gitignore"); got != setup.Gitignore {
		t.Errorf(".gitignore:\n%s", got)
	}
	if got := gittest.Run(t, f.remote, "ls-tree", "--name-only", "cache/jira"); got != "keep" {
		t.Errorf("branch changed: %s", got)
	}
}

func TestRunWithExistingConfigAsksForMissingWorkflow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.DeploymentType = "DataCenter"
	f.jira.Inject(jiramock.Fault{Path: "/rest/api/2/myself", Status: http.StatusOK,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: `{"name":"svc","displayName":""}`})
	f.write(t, ".jira-git-cache/config.yml", "jira_url: "+f.jira.URL+"\nprojects: [PLAT]\nfields: {customfield_10002: points}\n")
	f.opts.NonInteractive = false
	f.opts.Answers = setup.Answers{RunnerLabels: ptr("linux"), CreateBranch: ptr("yes")}

	out := f.mustRun(t)

	want := `✓ Authenticated as svc on Jira Data Center 10.3.4

✓ PLAT  1 issues in window

Runner labels [self-hosted]: linux

Kept .jira-git-cache/config.yml (already exists)
Wrote .jira-git-cache/.gitignore
Wrote .github/workflows/jira-sync.yaml

Create dev/jira-cache on origin with an empty snapshot? [Y/n] yes
✓ Pushed dev/jira-cache (never synced)

` + nextSteps("dev/jira-cache")
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if got := f.file(t, ".github/workflows/jira-sync.yaml"); got != string(setup.Workflow([]string{"linux"}, "v0.1.0")) {
		t.Errorf("workflow:\n%s", got)
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := gittest.Run(t, f.remote, "show", "dev/jira-cache:_meta/snapshot.json"); !strings.Contains(got, `"fields_hash": "`+cfg.FieldsHash()+`"`) {
		t.Errorf("snapshot.json:\n%s", got)
	}
}

func TestRunNamesChosenFields(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	custom := func(id int, name, typ string) map[string]any {
		return map[string]any{"id": fmt.Sprint("customfield_", id), "name": name, "custom": true,
			"schema": map[string]any{"type": "string", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:" + typ}}
	}
	f.jira.Fields = []map[string]any{
		custom(10002, "STORY POINTS", "float"),
		custom(10500, "Team", "select"),
		custom(10900, "2nd Reviewer", "userpicker"),
		custom(10901, "###", "textfield"),
		custom(10902, "team", "textfield"),
		custom(10903, "Alpha", "textfield"),
		custom(10904, "Équipe de développement", "textfield"),
	}
	first := jiramock.NewIssue("PLAT-1", 10001, jiramock.DefaultNow.Add(-time.Hour))
	for _, id := range []string{"10002", "10500", "10900", "10901", "10902", "10903", "10904", "10999"} {
		first.Fields["customfield_"+id] = "x"
	}
	second := jiramock.NewIssue("PLAT-2", 10002, jiramock.DefaultNow)
	second.Fields["customfield_10903"] = "y"
	f.jira.Put(first, second)
	f.opts.Answers.Fields = ptr("1-8")

	out := f.mustRun(t)

	wantTable := `Custom fields set on 2 recent issues:
     id                 Jira name                set on
  1  customfield_10903  Alpha                        2
  2  customfield_10002  STORY POINTS                 1  *
  3  customfield_10500  Team                         1
  4  customfield_10900  2nd Reviewer                 1
  5  customfield_10901  ###                          1
  6  customfield_10902  team                         1
  7  customfield_10904  Équipe de développement      1
  8  customfield_10999                               1
Include which fields? [2]: 1-8
`
	if !strings.Contains(out, wantTable) {
		t.Errorf("stdout:\n%s\nwant table:\n%s", out, wantTable)
	}
	want := `fields:
  customfield_10903: alpha
  customfield_10002: story_points
  customfield_10500: team
  customfield_10900: field_10900
  customfield_10901: field_10901
  customfield_10902: team_10902
  customfield_10904: quipe_de_d_veloppement
  customfield_10999: field_10999
`
	if got := f.file(t, ".jira-git-cache/config.yml"); !strings.HasSuffix(got, want) {
		t.Errorf("config.yml:\n%s", got)
	}
}

func TestRunDefaultsToNoFieldsWhenNoneIsPreselected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Put(jiramock.NewIssue("PLAT-2", 10002, jiramock.DefaultNow))
	team := jiramock.NewIssue("PLAT-1", 10001, jiramock.DefaultNow.Add(-time.Hour))
	team.Fields["customfield_10500"] = map[string]any{"value": "Platform"}
	f.jira.Put(team)
	f.opts.NonInteractive = false
	f.opts.Answers = setup.Answers{JiraURL: ptr(f.jira.URL), Projects: ptr("PLAT"), Days: ptr("1"), UnresolvedDays: ptr("1"),
		RunnerLabels: ptr(""), CacheBranch: ptr(""), CreateBranch: ptr("false")}
	f.opts.Stdin = strings.NewReader("\n")

	out := f.mustRun(t)

	for _, want := range []string{
		"Keep every issue updated within days [30]: 1\n",
		"Custom fields set on 2 recent issues:\n",
		"  1  customfield_10500  Team           1\nInclude which fields? [none]: \n",
		"Skipped dev/jira-cache\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if got := f.file(t, ".jira-git-cache/config.yml"); !strings.Contains(got, "  days: 1\n  unresolved_days: 1\n") || !strings.HasSuffix(got, "fields: {}\n") {
		t.Errorf("config.yml:\n%s", got)
	}
}

// failOnce makes the first request for path whose query has key=value fail with a 404.
func failOnce(srv *jiramock.Server, path, key, value string) {
	srv.OnRequest = func(r jiramock.Request) {
		if r.Path == path && r.Query.Get(key) == value {
			srv.Inject(jiramock.Fault{Path: path, Status: http.StatusNotFound, Times: 1})
		}
	}
}

func TestRunFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *fixture)
		want string
	}{
		{"unpinnable version", func(_ *testing.T, f *fixture) { f.opts.ToolVersion = "v0.1.0+dirty" },
			"cannot pin version v0.1.0+dirty in the workflow; pass --tool-version vX.Y.Z"},
		{"no token", func(_ *testing.T, f *fixture) { f.opts.Token = "" }, "JIRA_TOKEN is required with --non-interactive"},
		{"not a repo", func(t *testing.T, f *fixture) { f.opts.Dir = t.TempDir() }, "not a git repository"},
		{"no origin", func(t *testing.T, f *fixture) { gittest.Run(t, f.dir, "remote", "remove", "origin") }, "ls-remote"},
		{"default branch", func(t *testing.T, f *fixture) {
			gittest.Seed(t, f.remote, "trunk", nil)
			gittest.Run(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
		}, "origin's default branch is trunk; setup needs main"},
		{"invalid existing config", func(t *testing.T, f *fixture) { f.write(t, ".jira-git-cache/config.yml", "jira_url: x\n") },
			`config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "x"`},
		{"missing url", func(_ *testing.T, f *fixture) { f.opts.Answers.JiraURL = nil }, "--jira-url is required with --non-interactive"},
		{"bad url", func(_ *testing.T, f *fixture) { f.opts.Answers.JiraURL = ptr("ftp://jira") },
			`config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "ftp://jira"`},
		{"input ended", func(_ *testing.T, f *fixture) { f.opts.NonInteractive, f.opts.Answers = false, setup.Answers{} }, "setup: input ended"},
		{"token unreadable", func(_ *testing.T, f *fixture) {
			f.opts.NonInteractive, f.opts.Token = false, ""
			f.opts.ReadPassword = func(string) (string, error) { return "", errors.New("not a terminal") }
		}, "read token: not a terminal"},
		{"cloud", func(_ *testing.T, f *fixture) { f.jira.DeploymentType = "Cloud" }, "jira-git-cache supports Jira Data Center, not Cloud"},
		{"server info", func(_ *testing.T, f *fixture) {
			f.jira.Inject(jiramock.Fault{Path: "/rest/api/2/serverInfo", Status: 404})
		},
			"GET /rest/api/2/serverInfo: 404: Not Found"},
		{"missing projects", func(_ *testing.T, f *fixture) { f.opts.Answers.Projects = nil }, "--projects is required with --non-interactive"},
		{"duplicate project", func(_ *testing.T, f *fixture) { f.opts.Answers.Projects = ptr("PLAT, plat") }, `config.yml: projects: duplicate key "PLAT"`},
		{"days not a number", func(_ *testing.T, f *fixture) { f.opts.NonInteractive, f.opts.Answers.Days = false, ptr("x") },
			`"x" is not a whole number of days`},
		{"days above unresolved default", func(_ *testing.T, f *fixture) { f.opts.Answers.Days = ptr("181") },
			"config.yml: window.unresolved_days: must be at least window.days (181)"},
		{"unresolved not a number", func(_ *testing.T, f *fixture) { f.opts.Answers.UnresolvedDays = ptr("1.5") },
			`"1.5" is not a whole number of days`},
		{"unknown project", func(_ *testing.T, f *fixture) { f.opts.Answers.Projects = ptr("PLAT,PLATX") },
			"PLATX: The value 'PLATX' does not exist for the field 'project'."},
		{"count error without messages", func(_ *testing.T, f *fixture) { failOnce(f.jira, "/rest/api/2/search", "maxResults", "1") },
			"PLAT: GET /rest/api/2/search: 404: Not Found"},
		{"runner label", func(_ *testing.T, f *fixture) { f.opts.Answers.RunnerLabels = ptr("a,,b") }, `invalid runner label ""`},
		{"cache branch", func(_ *testing.T, f *fixture) { f.opts.Answers.CacheBranch = ptr("main") }, "config.yml: cache_branch: must not be main"},
		{"sample search", func(_ *testing.T, f *fixture) { failOnce(f.jira, "/rest/api/2/search", "fields", "*all") },
			"GET /rest/api/2/search: 404: Not Found"},
		{"field list", func(_ *testing.T, f *fixture) { f.jira.Inject(jiramock.Fault{Path: "/rest/api/2/field", Status: 404}) },
			"GET /rest/api/2/field: 404: Not Found"},
		{"malformed sample issue", func(_ *testing.T, f *fixture) {
			iss := jiramock.NewIssue("PLAT-1", 10001, jiramock.DefaultNow)
			delete(iss.Fields, "summary")
			f.jira.Put(iss)
		}, "PLAT-1: missing summary"},
		{"field selection", func(_ *testing.T, f *fixture) {
			f.jira.Put(jiramock.ExampleIssue())
			f.opts.Answers.Fields = ptr("9")
		}, `invalid selection "9": use numbers 1-5, ranges like 1-3, commas, or none`},
		{"unwritable", func(t *testing.T, f *fixture) { f.write(t, ".github", "file\n") }, "create directory: "},
		{"create branch answer", func(_ *testing.T, f *fixture) { f.opts.Answers.CreateBranch = ptr("maybe") }, `invalid answer "maybe": use y or n`},
		{"push rejected", func(t *testing.T, f *fixture) {
			hook := filepath.Join(f.remote, "hooks", "pre-receive")
			if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "pre-receive hook declined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			tc.edit(t, f)
			err := f.run(t)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q\noutput:\n%s", err, tc.want, f.out)
			}
		})
	}
}

func TestRunRejectsBadToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.opts.Token = "wrong"
	if err := f.run(t); !errors.Is(err, jira.ErrUnauthenticated) {
		t.Errorf("got %v, want ErrUnauthenticated", err)
	}
}
