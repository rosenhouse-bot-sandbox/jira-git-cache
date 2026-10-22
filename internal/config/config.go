// Package config reads, validates and writes .jira-git-cache/config.yml.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
)

// Paths relative to the repo's top level.
const (
	Dir  = ".jira-git-cache"
	File = ".jira-git-cache/config.yml"
)

// ErrNotFound means no directory between the start and the git top level holds File.
var ErrNotFound = errors.New("no .jira-git-cache/config.yml in this repo; run jira-git-cache setup, or use a clone of a cache repo")

var (
	projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	fieldID    = regexp.MustCompile(`^customfield_[0-9]+$`)
	fieldName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Window selects issues updated within Days, or unresolved and updated within UnresolvedDays.
type Window struct {
	Days           int `yaml:"days"`
	UnresolvedDays int `yaml:"unresolved_days"`
}

// Field maps a Jira custom field ID to its name in the snapshot.
type Field struct{ ID, Name string }

// Fields is a YAML mapping of ID to name that keeps its order.
type Fields []Field

// UnmarshalYAML reads a mapping of ID to name.
func (fs *Fields) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return errors.New("fields: must be a mapping of id to name")
	}
	var out Fields
	for i := 0; i < len(n.Content); i += 2 {
		id, name := n.Content[i], n.Content[i+1]
		if !isString(id) || !isString(name) {
			return fmt.Errorf("fields: %q: id and name must be strings", id.Value)
		}
		out = append(out, Field{ID: id.Value, Name: name.Value})
	}
	*fs = out
	return nil
}

// MarshalYAML writes a mapping of ID to name.
func (fs Fields) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, f := range fs {
		n.Content = append(n.Content, str(f.ID), str(f.Name))
	}
	return n, nil
}

func isString(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str"
}

func str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// Duration is a time.Duration written as a string such as "24h".
type Duration time.Duration

// UnmarshalYAML accepts only a string that time.ParseDuration parses.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil || !isString(n) {
		return fmt.Errorf("full_refresh_every: invalid duration %q (use h, m or s, e.g. 24h)", n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes d with textfmt.Duration.
func (d Duration) MarshalYAML() (any, error) {
	return textfmt.Duration(time.Duration(d)), nil
}

// Config is the content of config.yml.
type Config struct {
	JiraURL          string   `yaml:"jira_url"`
	CacheBranch      string   `yaml:"cache_branch"`
	Projects         []string `yaml:"projects,flow"`
	Window           Window   `yaml:"window"`
	FullRefreshEvery Duration `yaml:"full_refresh_every"`
	Concurrency      int      `yaml:"concurrency"`
	Fields           Fields   `yaml:"fields"`
}

// Default returns the values used for keys that config.yml omits.
func Default() Config {
	return Config{
		CacheBranch:      "dev/jira-cache",
		Window:           Window{Days: 30, UnresolvedDays: 180},
		FullRefreshEvery: Duration(24 * time.Hour),
		Concurrency:      8,
	}
}

// FindRoot walks up from dir to top, inclusive, and returns the first directory containing File.
func FindRoot(dir, top string) (string, error) {
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("find config: %w", err)
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return "", fmt.Errorf("find config: %w", err)
	}
	if rel, err := filepath.Rel(top, dir); err != nil || !filepath.IsLocal(rel) {
		return "", ErrNotFound
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, File)); err == nil {
			return dir, nil
		}
		if dir == top {
			return "", ErrNotFound
		}
		dir = filepath.Dir(dir)
	}
}

// Load reads and parses root/File.
func Load(root string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(root, File)) //nolint:gosec // Reading config from a caller-chosen repo is the point.
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return Parse(data)
}

// Parse decodes config.yml over Default(), rejecting unknown keys, then validates it.
func Parse(data []byte) (Config, error) {
	c := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config.yml: %w", err)
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config.yml: must hold one YAML document")
	}
	c.JiraURL = strings.TrimSuffix(c.JiraURL, "/")
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate returns the first problem with c, as "config.yml: <key>: <problem>".
func (c Config) Validate() error {
	if err := c.validate(); err != nil {
		return fmt.Errorf("config.yml: %w", err)
	}
	return nil
}

func (c Config) validate() error {
	if u, err := url.Parse(c.JiraURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(c.JiraURL, "?#") {
		return fmt.Errorf("jira_url: must be an absolute http or https URL with no query or fragment: %q", c.JiraURL)
	}
	switch c.CacheBranch {
	case "":
		return errors.New("cache_branch: must not be empty")
	case "main":
		return errors.New("cache_branch: must not be main")
	}
	if err := validateProjects(c.Projects); err != nil {
		return err
	}
	if c.Window.Days < 1 {
		return errors.New("window.days: must be at least 1")
	}
	if c.Window.UnresolvedDays < c.Window.Days {
		return fmt.Errorf("window.unresolved_days: must be at least window.days (%d)", c.Window.Days)
	}
	if d := time.Duration(c.FullRefreshEvery); d <= 0 {
		return errors.New("full_refresh_every: must be greater than 0")
	} else if d%time.Second != 0 {
		return errors.New("full_refresh_every: must be a whole number of seconds")
	}
	if c.Concurrency < 1 || c.Concurrency > 64 {
		return errors.New("concurrency: must be between 1 and 64")
	}
	return validateFields(c.Fields)
}

func validateProjects(projects []string) error {
	if len(projects) == 0 {
		return errors.New("projects: must list at least one project")
	}
	seen := map[string]bool{}
	for _, p := range projects {
		if !projectKey.MatchString(p) {
			return fmt.Errorf("projects: invalid key %q", p)
		}
		if seen[p] {
			return fmt.Errorf("projects: duplicate key %q", p)
		}
		seen[p] = true
	}
	return nil
}

func validateFields(fields Fields) error {
	ids, names := map[string]bool{}, map[string]bool{}
	for _, f := range fields {
		switch {
		case !fieldID.MatchString(f.ID):
			return fmt.Errorf("fields: invalid id %q", f.ID)
		case !fieldName.MatchString(f.Name):
			return fmt.Errorf("fields: %s: invalid name %q", f.ID, f.Name)
		case ids[f.ID]:
			return fmt.Errorf("fields: duplicate id %q", f.ID)
		case names[f.Name]:
			return fmt.Errorf("fields: duplicate name %q", f.Name)
		}
		ids[f.ID], names[f.Name] = true, true
	}
	return nil
}

// Marshal returns c as config.yml.
func (c Config) Marshal() []byte {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(c)
	_ = enc.Close()
	return b.Bytes()
}

// WindowJQL returns the JQL that enumerates project's issues in the window.
func (c Config) WindowJQL(project string) string {
	return fmt.Sprintf(`project = "%s" AND (updated >= "-%dd" OR (resolution = Unresolved AND updated >= "-%dd")) ORDER BY key`,
		project, c.Window.Days, c.Window.UnresolvedDays)
}

// SampleJQL returns the JQL for the most recently updated issues of projects.
func SampleJQL(projects []string) string {
	return `project in ("` + strings.Join(projects, `", "`) + `") ORDER BY updated DESC`
}

// FieldNames maps each configured field ID to its name.
func (c Config) FieldNames() map[string]string {
	names := make(map[string]string, len(c.Fields))
	for _, f := range c.Fields {
		names[f.ID] = f.Name
	}
	return names
}

// FieldIDs returns the configured field IDs in config order.
func (c Config) FieldIDs() []string {
	ids := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		ids[i] = f.ID
	}
	return ids
}

// FieldsHash identifies the field mapping regardless of order.
func (c Config) FieldsHash() string {
	lines := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		lines[i] = f.ID + "=" + f.Name + "\n"
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
