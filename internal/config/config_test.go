package config_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
)

const example = `jira_url: https://jira.example.com
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
`

const minimal = "jira_url: https://jira.example.com\nprojects: [PLAT]\n"

func exampleConfig() config.Config {
	return config.Config{
		JiraURL:          "https://jira.example.com",
		CacheBranch:      "dev/jira-cache",
		Projects:         []string{"PLAT", "INFRA"},
		Window:           config.Window{Days: 30, UnresolvedDays: 180},
		FullRefreshEvery: config.Duration(24 * time.Hour),
		Concurrency:      8,
		Fields: config.Fields{
			{ID: "customfield_10002", Name: "story_points"},
			{ID: "customfield_10100", Name: "sprint"},
		},
	}
}

func mustParse(t *testing.T, data string) config.Config {
	t.Helper()
	c, err := config.Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse(%q): %v", data, err)
	}
	return c
}

func TestDefault(t *testing.T) {
	want := config.Config{
		CacheBranch:      "dev/jira-cache",
		Window:           config.Window{Days: 30, UnresolvedDays: 180},
		FullRefreshEvery: config.Duration(24 * time.Hour),
		Concurrency:      8,
	}
	if got := config.Default(); !reflect.DeepEqual(got, want) {
		t.Errorf("Default() = %+v, want %+v", got, want)
	}
}

func TestParseExample(t *testing.T) {
	if got, want := mustParse(t, example), exampleConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

func TestParseFillsDefaults(t *testing.T) {
	want := config.Default()
	want.JiraURL = "https://jira.example.com"
	want.Projects = []string{"PLAT"}
	if got := mustParse(t, minimal); !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

func TestParseKeepsDefaultsInsidePartialWindow(t *testing.T) {
	got := mustParse(t, minimal+"window: {days: 7}\n").Window
	if want := (config.Window{Days: 7, UnresolvedDays: 180}); got != want {
		t.Errorf("Window = %+v, want %+v", got, want)
	}
}

func TestParseTrimsOneTrailingSlash(t *testing.T) {
	for in, want := range map[string]string{
		"https://jira.example.com/":       "https://jira.example.com",
		"https://jira.example.com/jira/":  "https://jira.example.com/jira",
		"http://jira.example.com:8080//":  "http://jira.example.com:8080/",
		"https://jira.example.com/jira":   "https://jira.example.com/jira",
		"HTTPS://jira.example.com/":       "HTTPS://jira.example.com",
		"https://user@jira.example.com/a": "https://user@jira.example.com/a",
	} {
		if got := mustParse(t, "jira_url: "+in+"\nprojects: [PLAT]\n").JiraURL; got != want {
			t.Errorf("jira_url %q parsed as %q, want %q", in, got, want)
		}
	}
}

func TestParseAcceptsBoundaries(t *testing.T) {
	for _, extra := range []string{
		"concurrency: 1\n",
		"concurrency: 64\n",
		"window: {days: 1, unresolved_days: 1}\n",
		"window: {days: 30, unresolved_days: 30}\n",
		"full_refresh_every: 1s\n",
		"projects: [A, B_2, C9]\n",
		"fields:\n",
		"fields: ~\n",
		"fields: {}\n",
		"fields: {customfield_1: a, customfield_22: b_2_c}\n",
	} {
		data := "jira_url: https://jira.example.com\n" + extra
		if !strings.HasPrefix(extra, "projects:") {
			data += "projects: [PLAT]\n"
		}
		if _, err := config.Parse([]byte(data)); err != nil {
			t.Errorf("Parse(%q): %v", data, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{"", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: ""`},
		{"projects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: ""`},
		{"jira_url: jira.example.com\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "jira.example.com"`},
		{"jira_url: ftp://jira.example.com\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "ftp://jira.example.com"`},
		{"jira_url: https:///path\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "https:///path"`},
		{"jira_url: https://jira.example.com?x=1\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "https://jira.example.com?x=1"`},
		{"jira_url: https://jira.example.com?\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "https://jira.example.com?"`},
		{"jira_url: 'https://jira.example.com#'\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "https://jira.example.com#"`},
		{"jira_url: 'https://jira.example.com/%zz'\nprojects: [PLAT]\n", `config.yml: jira_url: must be an absolute http or https URL with no query or fragment: "https://jira.example.com/%zz"`},
		{minimal + "---\nbogus: 1\n", "config.yml: must hold one YAML document"},
		{minimal + "cache_branch: ''\n", "config.yml: cache_branch: must not be empty"},
		{minimal + "cache_branch: main\n", "config.yml: cache_branch: must not be main"},
		{"jira_url: https://jira.example.com\n", "config.yml: projects: must list at least one project"},
		{"jira_url: https://jira.example.com\nprojects: []\n", "config.yml: projects: must list at least one project"},
		{"jira_url: https://jira.example.com\nprojects: [plat]\n", `config.yml: projects: invalid key "plat"`},
		{"jira_url: https://jira.example.com\nprojects: [PLAT, 1PLAT]\n", `config.yml: projects: invalid key "1PLAT"`},
		{"jira_url: https://jira.example.com\nprojects: [PLAT-1]\n", `config.yml: projects: invalid key "PLAT-1"`},
		{"jira_url: https://jira.example.com\nprojects: [PLAT, INFRA, PLAT]\n", `config.yml: projects: duplicate key "PLAT"`},
		{minimal + "window: {days: 0, unresolved_days: 0}\n", "config.yml: window.days: must be at least 1"},
		{minimal + "window: {days: 30, unresolved_days: 29}\n", "config.yml: window.unresolved_days: must be at least window.days (30)"},
		{minimal + "full_refresh_every: 1d\n", `config.yml: full_refresh_every: invalid duration "1d" (use h, m or s, e.g. 24h)`},
		{minimal + "full_refresh_every: 0\n", `config.yml: full_refresh_every: invalid duration "0" (use h, m or s, e.g. 24h)`},
		{minimal + "full_refresh_every: {h: 1}\n", `config.yml: full_refresh_every: invalid duration "" (use h, m or s, e.g. 24h)`},
		{minimal + "full_refresh_every: 0s\n", "config.yml: full_refresh_every: must be greater than 0"},
		{minimal + "full_refresh_every: -1h\n", "config.yml: full_refresh_every: must be greater than 0"},
		{minimal + "full_refresh_every: 1500ms\n", "config.yml: full_refresh_every: must be a whole number of seconds"},
		{minimal + "concurrency: 0\n", "config.yml: concurrency: must be between 1 and 64"},
		{minimal + "concurrency: 65\n", "config.yml: concurrency: must be between 1 and 64"},
		{minimal + "fields: [customfield_1]\n", "config.yml: fields: must be a mapping of id to name"},
		{minimal + "fields: sprint\n", "config.yml: fields: must be a mapping of id to name"},
		{minimal + "fields: {customfield_1: true}\n", `config.yml: fields: "customfield_1": id and name must be strings`},
		{minimal + "fields: {customfield_1: }\n", `config.yml: fields: "customfield_1": id and name must be strings`},
		{minimal + "fields: {customfield_1: [a]}\n", `config.yml: fields: "customfield_1": id and name must be strings`},
		{minimal + "fields: {10002: story_points}\n", `config.yml: fields: "10002": id and name must be strings`},
		{minimal + "fields: {customfield_1: &n sprint, customfield_2: *n}\n", `config.yml: fields: "customfield_2": id and name must be strings`},
		{minimal + "fields: {customfield_x: sprint}\n", `config.yml: fields: invalid id "customfield_x"`},
		{minimal + "fields: {customfield_: sprint}\n", `config.yml: fields: invalid id "customfield_"`},
		{minimal + "fields: {xcustomfield_1: sprint}\n", `config.yml: fields: invalid id "xcustomfield_1"`},
		{minimal + "fields: {customfield_1: Sprint}\n", `config.yml: fields: customfield_1: invalid name "Sprint"`},
		{minimal + "fields: {customfield_1: 1sprint}\n", `config.yml: fields: customfield_1: invalid name "1sprint"`},
		{minimal + "fields: {customfield_1: story-points}\n", `config.yml: fields: customfield_1: invalid name "story-points"`},
		{minimal + "fields: {customfield_1: a, customfield_01: b, customfield_1: c}\n", `config.yml: fields: duplicate id "customfield_1"`},
		{minimal + "fields: {customfield_1: sprint, customfield_2: sprint}\n", `config.yml: fields: duplicate name "sprint"`},
	} {
		_, err := config.Parse([]byte(tc.data))
		if err == nil || err.Error() != tc.want {
			t.Errorf("Parse(%q) error = %v, want %q", tc.data, err, tc.want)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	for _, data := range []string{
		minimal + "bogus: 1\n",
		minimal + "window: {days: 30, unresolved: 180}\n",
		"jira_url: [\n",
	} {
		c, err := config.Parse([]byte(data))
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want error", data)
		}
		if !reflect.DeepEqual(c, config.Config{}) {
			t.Errorf("Parse(%q) = %+v, want zero Config on error", data, c)
		}
	}
}

func TestParseReturnsZeroConfigWhenInvalid(t *testing.T) {
	c, err := config.Parse([]byte(minimal + "concurrency: 0\n"))
	if err == nil || !reflect.DeepEqual(c, config.Config{}) {
		t.Errorf("Parse = %+v, %v; want zero Config and an error", c, err)
	}
}

func TestValidate(t *testing.T) {
	c := exampleConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}
	c.Concurrency = 65
	if err := c.Validate(); err == nil {
		t.Error("Validate() accepted concurrency 65")
	}
}

func TestMarshal(t *testing.T) {
	if got := string(exampleConfig().Marshal()); got != example {
		t.Errorf("Marshal() =\n%s\nwant\n%s", got, example)
	}
}

func TestMarshalEmptyFields(t *testing.T) {
	c := exampleConfig()
	c.Fields = nil
	c.FullRefreshEvery = config.Duration(90 * time.Minute)
	want := `jira_url: https://jira.example.com
cache_branch: dev/jira-cache
projects: [PLAT, INFRA]
window:
  days: 30
  unresolved_days: 180
full_refresh_every: 1h30m
concurrency: 8
fields: {}
`
	if got := string(c.Marshal()); got != want {
		t.Errorf("Marshal() =\n%s\nwant\n%s", got, want)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	odd := exampleConfig()
	odd.JiraURL = "https://jira.example.com/a: b"
	odd.CacheBranch = "no"
	odd.Projects = []string{"NO", "Y", "USER"}
	odd.Fields = config.Fields{{ID: "customfield_9", Name: "true"}, {ID: "customfield_10", Name: "null"}}
	odd.FullRefreshEvery = config.Duration(time.Hour + 30*time.Second)
	for _, c := range []config.Config{exampleConfig(), odd, mustParse(t, minimal)} {
		got, err := config.Parse(c.Marshal())
		if err != nil {
			t.Fatalf("Parse(Marshal()): %v\n%s", err, c.Marshal())
		}
		if !reflect.DeepEqual(got, c) {
			t.Errorf("Parse(Marshal()) = %+v, want %+v", got, c)
		}
	}
}

func TestWindowJQL(t *testing.T) {
	c := exampleConfig()
	for project, want := range map[string]string{
		"PLAT": `project = "PLAT" AND (updated >= "-30d" OR (resolution = Unresolved AND updated >= "-180d")) ORDER BY key`,
		"USER": `project = "USER" AND (updated >= "-30d" OR (resolution = Unresolved AND updated >= "-180d")) ORDER BY key`,
	} {
		if got := c.WindowJQL(project); got != want {
			t.Errorf("WindowJQL(%q) = %q, want %q", project, got, want)
		}
	}
	c.Window = config.Window{Days: 7, UnresolvedDays: 90}
	if got, want := c.WindowJQL("INFRA"), `project = "INFRA" AND (updated >= "-7d" OR (resolution = Unresolved AND updated >= "-90d")) ORDER BY key`; got != want {
		t.Errorf("WindowJQL = %q, want %q", got, want)
	}
}

func TestSampleJQL(t *testing.T) {
	for _, tc := range []struct {
		projects []string
		want     string
	}{
		{[]string{"PLAT", "INFRA"}, `project in ("PLAT", "INFRA") ORDER BY updated DESC`},
		{[]string{"USER"}, `project in ("USER") ORDER BY updated DESC`},
	} {
		if got := config.SampleJQL(tc.projects); got != tc.want {
			t.Errorf("SampleJQL(%q) = %q, want %q", tc.projects, got, tc.want)
		}
	}
}

func TestFieldNamesAndIDs(t *testing.T) {
	c := exampleConfig()
	wantNames := map[string]string{"customfield_10002": "story_points", "customfield_10100": "sprint"}
	if got := c.FieldNames(); !reflect.DeepEqual(got, wantNames) {
		t.Errorf("FieldNames() = %v, want %v", got, wantNames)
	}
	c.Fields[0], c.Fields[1] = c.Fields[1], c.Fields[0]
	if got, want := c.FieldIDs(), []string{"customfield_10100", "customfield_10002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FieldIDs() = %v, want %v", got, want)
	}
}

func TestFieldsHash(t *testing.T) {
	c := exampleConfig()
	sum := sha256.Sum256([]byte("customfield_10002=story_points\ncustomfield_10100=sprint\n"))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got := c.FieldsHash(); got != want {
		t.Errorf("FieldsHash() = %q, want %q", got, want)
	}
	c.Fields[0], c.Fields[1] = c.Fields[1], c.Fields[0]
	if got := c.FieldsHash(); got != want {
		t.Errorf("reordered FieldsHash() = %q, want %q", got, want)
	}
	c.Fields[0].Name = "sprints"
	if got := c.FieldsHash(); got == want {
		t.Error("renaming a field kept FieldsHash()")
	}
}

func writeConfig(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, config.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, config.File), []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	top := mkdir(t, base, "repo")
	sub := mkdir(t, top, "a", "b")
	writeConfig(t, top)
	nested := mkdir(t, top, "nested")
	writeConfig(t, nested)
	link := filepath.Join(base, "link")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{top, sub, filepath.Join(top, config.Dir), link} {
		if got, err := config.FindRoot(dir, top); err != nil || got != top {
			t.Errorf("FindRoot(%q) = %q, %v; want %q", dir, got, err, top)
		}
	}
	if got, err := config.FindRoot(mkdir(t, nested, "x"), top); err != nil || got != nested {
		t.Errorf("FindRoot(nested) = %q, %v; want %q", got, err, nested)
	}
}

func TestFindRootNotFound(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base)
	top := mkdir(t, base, "repo")
	sub := mkdir(t, top, "a")
	other := mkdir(t, base, "other")
	for _, dir := range []string{top, sub, other, base} {
		if _, err := config.FindRoot(dir, top); !errors.Is(err, config.ErrNotFound) {
			t.Errorf("FindRoot(%q) error = %v, want ErrNotFound", dir, err)
		}
	}
}

func TestFindRootMissingDir(t *testing.T) {
	top := t.TempDir()
	for _, tc := range [][2]string{
		{filepath.Join(top, "missing"), top},
		{top, filepath.Join(top, "missing")},
	} {
		_, err := config.FindRoot(tc[0], tc[1])
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("FindRoot(%q, %q) error = %v, want ErrNotExist", tc[0], tc[1], err)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root)
	got, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := exampleConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := config.Load(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(empty dir) error = %v, want ErrNotExist", err)
	}
	mkdir(t, root, config.Dir)
	if err := os.WriteFile(filepath.Join(root, config.File), []byte("concurrency: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err == nil {
		t.Error("Load(invalid config) succeeded")
	}
}
