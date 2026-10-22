// Package setup bootstraps a cache repo: its config, .gitignore, workflow and empty cache branch.
package setup

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gitstore"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/version"
)

// Answers holds preset answers, as a user would type them at each prompt.
// nil means "not given": prompt, or in non-interactive mode use the default.
type Answers struct {
	JiraURL, Projects, Days, UnresolvedDays, RunnerLabels, CacheBranch, Fields, CreateBranch *string
}

// Options configures Run.
type Options struct {
	Dir            string
	Stdin          io.Reader // interactive answers, one per line
	Stdout         io.Writer
	Token          string // "" means ask ReadPassword, in interactive mode only
	ReadPassword   func(prompt string) (string, error)
	HTTP           *http.Client
	ToolVersion    string // pinned in the workflow, so it must be version.Pinnable
	Now            time.Time
	NonInteractive bool
	Answers        Answers
}

const origin = "origin"

var (
	runnerLabel = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// badBranch matches the names that git check-ref-format --branch rejects.
	badBranch = regexp.MustCompile(`^-|^HEAD$|^@$|(^|/)\.|\.lock(/|$)|\.\.|@\{|//|^/|/$|\.$|[\x00-\x20\x7f~^:?*\[\\]`)
)

// Run asks for the cache repo's settings, checks them against Jira, writes the
// repo's files and creates the cache branch.
func Run(ctx context.Context, opts Options) error {
	if !version.Pinnable(opts.ToolVersion) {
		return fmt.Errorf("cannot pin version %s in the workflow; pass --tool-version vX.Y.Z", opts.ToolVersion)
	}
	if opts.NonInteractive && opts.Token == "" {
		return errors.New("JIRA_TOKEN is required with --non-interactive")
	}
	repo, err := gitstore.Open(ctx, opts.Dir)
	if err != nil {
		return err
	}
	if branch, err := repo.DefaultBranch(ctx, origin); err != nil {
		return err
	} else if branch != "main" {
		return fmt.Errorf("origin's default branch is %s; setup needs main", branch)
	}
	s := &session{Options: opts, in: bufio.NewReader(opts.Stdin), blank: true}
	return s.run(ctx, repo)
}

type session struct {
	Options
	in    *bufio.Reader
	blank bool // nothing is written yet, or the output ends with a blank line
}

func (s *session) run(ctx context.Context, repo *gitstore.Repo) error {
	cfg, err := config.Load(repo.Dir)
	existing := !errors.Is(err, fs.ErrNotExist)
	if existing && err != nil {
		return err
	}
	if !existing {
		cfg = config.Default()
		if cfg.JiraURL, err = askValid(ctx, s, "Jira URL: ", "", "jira-url", s.Answers.JiraURL, parseURL); err != nil {
			return err
		}
	}
	client, err := s.authenticate(ctx, cfg.JiraURL)
	if err != nil {
		return err
	}
	if !existing {
		if err := s.askWindow(ctx, &cfg); err != nil {
			return err
		}
	}
	if err := s.countWindow(ctx, client, cfg); err != nil {
		return err
	}
	labels, err := s.askLabels(ctx, repo.Dir)
	if err != nil {
		return err
	}
	if !existing {
		if cfg.CacheBranch, err = askValid(ctx, s, fmt.Sprintf("Cache branch [%s]: ", cfg.CacheBranch), cfg.CacheBranch,
			"cache-branch", s.Answers.CacheBranch, parseBranch); err != nil {
			return err
		}
		s.paragraph()
		if cfg.Fields, err = s.chooseFields(ctx, client, cfg.Projects); err != nil {
			return err
		}
	}
	if err := answerError(cfg.Validate()); err != nil {
		return err
	}
	s.paragraph()
	if err := s.writeFiles(repo.Dir, cfg, labels); err != nil {
		return err
	}
	s.paragraph()
	if err := s.createBranch(ctx, repo, cfg); err != nil {
		return err
	}
	s.paragraph()
	s.printf(nextSteps, cfg.CacheBranch)
	return nil
}

func (s *session) printf(format string, args ...any) {
	fmt.Fprintf(s.Stdout, format, args...)
	s.blank = false
}

// paragraph ends the current group of lines with a blank line.
func (s *session) paragraph() {
	if !s.blank {
		fmt.Fprintln(s.Stdout)
		s.blank = true
	}
}

func (s *session) ask(ctx context.Context, prompt, def, flag string, preset *string) (string, error) {
	if preset != nil {
		s.printf("%s\n", strings.TrimRight(prompt+*preset, " "))
		return cmp.Or(strings.TrimSpace(*preset), def), nil
	}
	if s.NonInteractive {
		if def == "" {
			return "", fmt.Errorf("--%s is required with --non-interactive", flag)
		}
		return def, nil
	}
	s.printf("%s", prompt)
	line, err := s.readLine(ctx)
	if err != nil {
		return "", err
	}
	return cmp.Or(strings.TrimSpace(line), def), nil
}

// readLine returns early when ctx is done, because SIGINT only cancels ctx.
func (s *session) readLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := s.in.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("setup: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil && r.line == "" {
			return "", errors.New("setup: input ended")
		}
		return r.line, nil
	}
}

// askValid asks until parse accepts the answer. A preset or non-interactive answer gets one try.
func askValid[T any](ctx context.Context, s *session, prompt, def, flag string, preset *string, parse func(string) (T, error)) (T, error) {
	for {
		answer, err := s.ask(ctx, prompt, def, flag, preset)
		if err != nil {
			var zero T
			return zero, err
		}
		v, err := parse(answer)
		if err == nil || preset != nil || s.NonInteractive {
			return v, err
		}
		s.printf("%v\n", err)
	}
}

// validate checks one answer by applying edit to an otherwise valid config.
func validate(edit func(*config.Config)) error {
	c := config.Default()
	c.JiraURL, c.Projects = "https://jira.invalid", []string{"P"}
	edit(&c)
	return answerError(c.Validate())
}

// answerError drops Validate's "config.yml: " prefix, because config.yml is not written yet.
func answerError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.TrimPrefix(err.Error(), "config.yml: "))
}

func parseURL(v string) (string, error) {
	u := strings.TrimSuffix(v, "/")
	return u, validate(func(c *config.Config) { c.JiraURL = u })
}

func parseProjects(v string) ([]string, error) {
	projects := strings.Split(v, ",")
	for i, p := range projects {
		projects[i] = strings.ToUpper(strings.TrimSpace(p))
	}
	return projects, validate(func(c *config.Config) { c.Projects = projects })
}

func parseBranch(v string) (string, error) {
	if badBranch.MatchString(v) {
		return "", fmt.Errorf("cache_branch: invalid branch name %q", v)
	}
	return v, validate(func(c *config.Config) { c.CacheBranch = v })
}

func parseDays(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number of days", v)
	}
	return n, nil
}

func parseLabels(v string) ([]string, error) {
	labels := strings.Split(v, ",")
	for i, l := range labels {
		labels[i] = strings.TrimSpace(l)
		if !runnerLabel.MatchString(labels[i]) {
			return nil, fmt.Errorf("invalid runner label %q", labels[i])
		}
	}
	return labels, nil
}

func parseYes(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "y", "yes", "true":
		return true, nil
	case "n", "no", "false":
		return false, nil
	}
	return false, fmt.Errorf("invalid answer %q: use y or n", v)
}

func (s *session) authenticate(ctx context.Context, jiraURL string) (*jira.Client, error) {
	token := s.Token
	if token == "" {
		var err error
		if token, err = s.ReadPassword("Jira token (hidden; or set JIRA_TOKEN): "); err != nil {
			return nil, fmt.Errorf("read token: %w", err)
		}
	}
	client := &jira.Client{BaseURL: jiraURL, Token: token, HTTP: s.HTTP}
	me, err := client.Myself(ctx)
	if err != nil {
		return nil, err
	}
	info, err := client.ServerInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info.DeploymentType == "Cloud" {
		return nil, errors.New("jira-git-cache supports Jira Data Center, not Cloud")
	}
	s.printf("✓ Authenticated as %s on Jira Data Center %s\n", cmp.Or(me.DisplayName, me.Name), info.Version)
	s.paragraph()
	return client, nil
}

func (s *session) askWindow(ctx context.Context, cfg *config.Config) error {
	var err error
	if cfg.Projects, err = askValid(ctx, s, "Projects (comma-separated keys): ", "", "projects", s.Answers.Projects, parseProjects); err != nil {
		return err
	}
	prompt, def := fmt.Sprintf("Keep every issue updated within days [%d]: ", cfg.Window.Days), cfg.Window.Days
	parse := parseWindowDays(func(days int) config.Window { return config.Window{Days: days, UnresolvedDays: days} })
	if cfg.Window.Days, err = askValid(ctx, s, prompt, strconv.Itoa(def), "days", s.Answers.Days, parse); err != nil {
		return err
	}
	def = max(cfg.Window.UnresolvedDays, cfg.Window.Days)
	prompt = fmt.Sprintf("Also keep unresolved issues updated within days [%d]: ", def)
	parse = parseWindowDays(func(days int) config.Window { return config.Window{Days: cfg.Window.Days, UnresolvedDays: days} })
	cfg.Window.UnresolvedDays, err = askValid(ctx, s, prompt, strconv.Itoa(def), "unresolved-days", s.Answers.UnresolvedDays, parse)
	return err
}

// parseWindowDays returns a parser that checks its days as part of window(days).
func parseWindowDays(window func(days int) config.Window) func(string) (int, error) {
	return func(v string) (int, error) {
		days, err := parseDays(v)
		if err != nil {
			return 0, err
		}
		return days, validate(func(c *config.Config) { c.Window = window(days) })
	}
}

func (s *session) countWindow(ctx context.Context, client *jira.Client, cfg config.Config) error {
	counts := make([]string, len(cfg.Projects))
	keyW, countW := 0, 0
	for i, p := range cfg.Projects {
		page, err := client.SearchPage(ctx, cfg.WindowJQL(p), nil, 0, 1)
		if err != nil {
			return projectError(p, err)
		}
		counts[i] = textfmt.Issues(page.Total)
		n, _, _ := strings.Cut(counts[i], " ")
		keyW, countW = max(keyW, len(p)), max(countW, len(n))
	}
	for i, p := range cfg.Projects {
		n, noun, _ := strings.Cut(counts[i], " ")
		s.printf("✓ %-*s%*s %s in window\n", keyW+2, p, countW, n, noun)
	}
	s.paragraph()
	return nil
}

func projectError(project string, err error) error {
	var apiErr *jira.APIError
	if errors.As(err, &apiErr) && len(apiErr.Messages) > 0 {
		return fmt.Errorf("%s: %s", project, strings.Join(apiErr.Messages, "; "))
	}
	return fmt.Errorf("%s: %w", project, err)
}

// askLabels asks for runner labels only when the workflow is missing, because config.yml does not store them.
func (s *session) askLabels(ctx context.Context, top string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(top, workflowPath)); err == nil {
		return nil, nil
	}
	return askValid(ctx, s, "Runner labels [self-hosted]: ", "self-hosted", "runner-labels", s.Answers.RunnerLabels, parseLabels)
}

func (s *session) writeFiles(top string, cfg config.Config, labels []string) error {
	for _, f := range []struct {
		path string
		data []byte
	}{
		{config.File, cfg.Marshal()},
		{config.Dir + "/.gitignore", []byte(Gitignore)},
		{workflowPath, Workflow(labels, s.ToolVersion)},
	} {
		created, err := writeNew(filepath.Join(top, f.path), f.data)
		if err != nil {
			return err
		}
		if created {
			s.printf("Wrote %s\n", f.path)
		} else {
			s.printf("Kept %s (already exists)\n", f.path)
		}
	}
	return nil
}

// writeNew creates path with data, or reports false if path already exists.
func writeNew(path string, data []byte) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // The repo's own directories.
		return false, fmt.Errorf("create directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // A file to commit.
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create file: %w", err)
	}
	_, err = f.Write(data)
	if err = errors.Join(err, f.Close()); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func (s *session) createBranch(ctx context.Context, repo *gitstore.Repo, cfg config.Config) error {
	branch := cfg.CacheBranch
	exists, err := repo.RemoteHasBranch(ctx, origin, branch)
	if err != nil {
		return err
	}
	if exists {
		s.printf("✓ %s already exists on origin; left it alone\n", branch)
		return nil
	}
	create, err := askValid(ctx, s, fmt.Sprintf("Create %s on origin with an empty snapshot? [Y/n] ", branch), "y",
		"create-branch", s.Answers.CreateBranch, parseYes)
	if err != nil {
		return err
	}
	if !create {
		s.printf("Skipped %s\n", branch)
		return nil
	}
	empty := snapshot.Snapshot{Meta: snapshot.Meta{
		JiraURL:       cfg.JiraURL,
		Projects:      map[string]snapshot.ProjectMeta{},
		SchemaVersion: snapshot.SchemaVersion,
		ToolVersion:   s.ToolVersion,
		FieldsHash:    cfg.FieldsHash(),
	}}
	if _, err := repo.Publish(ctx, origin, branch, "", empty.Encode(), "setup: empty snapshot", s.Now); err != nil {
		return err
	}
	s.printf("✓ Pushed %s (never synced)\n", branch)
	return nil
}
