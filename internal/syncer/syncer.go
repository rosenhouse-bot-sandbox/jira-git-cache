// Package syncer builds a new snapshot from Jira, reusing unchanged issues of the previous one.
package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
)

// fullRefreshSlack keeps cron jitter from delaying the full refresh by a whole interval.
const fullRefreshSlack = 5 * time.Minute

var issueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)

// Options are the inputs of one sync.
type Options struct {
	Config      config.Config
	Jira        *jira.Client
	Previous    snapshot.Snapshot // zero when the branch is missing, never synced, or undecodable
	Full        bool
	Now         time.Time // run start
	ToolVersion string
}

// ProjectStats counts one project's issues.
type ProjectStats struct {
	Project                                 string
	InWindow, New, Changed, Reused, Removed int
}

// Result is the new snapshot and what the sync did.
type Result struct {
	Snapshot                 snapshot.Snapshot
	Projects                 []ProjectStats // config order
	Fetched, Reused, Removed int
	FullRefreshReason        string   // "" when not a full refresh
	Skipped                  []string // keys that left Jira or changed key between enumeration and fetch
}

type enumerated struct{ key, project, updated string }

// Run enumerates each project's window, then fetches new and changed issues.
func Run(ctx context.Context, opts Options) (Result, error) {
	cfg := opts.Config
	if _, err := opts.Jira.Myself(ctx); err != nil {
		return Result{}, fmt.Errorf("check token: %w", err)
	}
	fields, err := opts.Jira.Fields(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list fields: %w", err)
	}
	r := Result{FullRefreshReason: FullRefreshReason(opts)}
	full := r.FullRefreshReason != ""
	entries, err := enumerate(ctx, opts.Jira, cfg)
	if err != nil {
		return Result{}, err
	}
	issues, fetch := r.diff(cfg.Projects, entries, opts.Previous.Issues, full)

	f := &fetcher{
		jira:   opts.Jira,
		fields: append(slices.Clone(snapshot.SystemFields), cfg.FieldIDs()...),
		names:  cfg.FieldNames(),
		issues: issues,
	}
	if err := forEach(ctx, cfg.Concurrency, fetch, f.fetch); err != nil {
		return Result{}, fmt.Errorf("fetch issues: %w", err)
	}
	sortKeys(f.skipped)
	r.Skipped = f.skipped
	r.Fetched = len(fetch) - len(f.skipped)

	fetchedAt := opts.Now.UTC().Truncate(time.Second)
	lastFullAt := opts.Previous.Meta.LastFullAt
	if full {
		lastFullAt = &fetchedAt
	}
	projects := map[string]snapshot.ProjectMeta{}
	for _, p := range cfg.Projects {
		projects[p] = snapshot.ProjectMeta{JQL: cfg.WindowJQL(p)}
	}
	for _, e := range entries {
		if _, ok := issues[e.key]; ok {
			pm := projects[e.project]
			pm.Count++
			projects[e.project] = pm
		}
	}
	r.Snapshot = snapshot.Snapshot{
		Meta: snapshot.Meta{
			FetchedAt:     &fetchedAt,
			LastFullAt:    lastFullAt,
			JiraURL:       cfg.JiraURL,
			Projects:      projects,
			SchemaVersion: snapshot.SchemaVersion,
			ToolVersion:   opts.ToolVersion,
			FieldsHash:    cfg.FieldsHash(),
		},
		Fields: snapshot.FieldsFromJira(fields),
		Issues: issues,
	}
	return r, nil
}

// diff fills in r's counts. It returns the reused issues and the sorted keys to fetch.
func (r *Result) diff(projects []string, entries []enumerated, prev map[string]snapshot.Issue, full bool) (map[string]snapshot.Issue, []string) {
	r.Projects = make([]ProjectStats, len(projects))
	stats := map[string]*ProjectStats{}
	for i, p := range projects {
		r.Projects[i].Project = p
		stats[p] = &r.Projects[i]
	}
	enumeratedKeys := map[string]bool{}
	reused := map[string]snapshot.Issue{}
	var fetch []string
	for _, e := range entries {
		st := stats[e.project]
		st.InWindow++
		enumeratedKeys[e.key] = true
		old, ok := prev[e.key]
		if ok && !full && old.Updated == e.updated {
			st.Reused++
			r.Reused++
			reused[e.key] = old
			continue
		}
		if ok {
			st.Changed++
		} else {
			st.New++
		}
		fetch = append(fetch, e.key)
	}
	for key := range prev {
		if !enumeratedKeys[key] {
			r.Removed++
			if st, ok := stats[snapshot.Project(key)]; ok {
				st.Removed++
			}
		}
	}
	sortKeys(fetch)
	return reused, fetch
}

// FullRefreshReason says why Run will refetch every issue, or returns "".
func FullRefreshReason(opts Options) string {
	m, cfg := opts.Previous.Meta, opts.Config
	every := time.Duration(cfg.FullRefreshEvery)
	if opts.Full {
		return "--full"
	}
	if m.LastFullAt == nil {
		return "no previous full refresh"
	}
	if m.SchemaVersion != snapshot.SchemaVersion {
		return "schema_version changed"
	}
	if m.FieldsHash != cfg.FieldsHash() {
		return "fields changed"
	}
	if m.JiraURL != cfg.JiraURL {
		return "jira_url changed"
	}
	if opts.Now.Sub(*m.LastFullAt) >= every-fullRefreshSlack {
		return "last full refresh older than " + textfmt.Duration(every)
	}
	return ""
}

// enumerate lists each project's window in config order. A key listed twice keeps its first project.
func enumerate(ctx context.Context, c *jira.Client, cfg config.Config) ([]enumerated, error) {
	var out []enumerated
	seen := map[string]bool{}
	for _, p := range cfg.Projects {
		issues, err := c.SearchAll(ctx, cfg.WindowJQL(p), []string{"updated"})
		if err != nil {
			return nil, fmt.Errorf("enumerate %s: %w", p, err)
		}
		for _, iss := range issues {
			updated, err := updatedOf(iss)
			if err != nil {
				return nil, fmt.Errorf("enumerate %s: %w", p, err)
			}
			if !seen[iss.Key] {
				seen[iss.Key] = true
				out = append(out, enumerated{key: iss.Key, project: p, updated: updated})
			}
		}
	}
	return out, nil
}

func updatedOf(iss jira.Issue) (string, error) {
	if !issueKey.MatchString(iss.Key) {
		return "", fmt.Errorf("invalid issue key %q", iss.Key)
	}
	var s string
	if err := json.Unmarshal(iss.Fields["updated"], &s); err != nil {
		return "", fmt.Errorf("%s: updated: %w", iss.Key, err)
	}
	updated, err := snapshot.NormalizeTime(s)
	if err != nil {
		return "", fmt.Errorf("%s: updated: %w", iss.Key, err)
	}
	return updated, nil
}

func sortKeys(keys []string) {
	sort.Slice(keys, func(i, j int) bool { return snapshot.LessKey(keys[i], keys[j]) })
}

type fetcher struct {
	jira   *jira.Client
	fields []string
	names  map[string]string

	mu      sync.Mutex
	issues  map[string]snapshot.Issue
	skipped []string
}

func (f *fetcher) fetch(ctx context.Context, key string) error {
	iss, err := f.jira.GetIssue(ctx, key, f.fields)
	if err != nil || iss.Key != key {
		return f.skip(key, err)
	}
	comments, err := f.jira.Comments(ctx, key)
	if err != nil {
		return f.skip(key, err)
	}
	out, err := snapshot.FromJira(iss, comments, f.names)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[key] = out
	return nil
}

// skip records that key left Jira. It returns err instead when err is neither nil nor a 404.
func (f *fetcher) skip(key string, err error) error {
	var apiErr *jira.APIError
	if err != nil && (!errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound) {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipped = append(f.skipped, key)
	return nil
}
