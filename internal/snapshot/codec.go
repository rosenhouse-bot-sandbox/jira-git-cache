package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// SchemaVersion is the version of the branch's file format that this build writes.
const SchemaVersion = 1

const (
	metaPath       = "_meta/snapshot.json"
	fieldsPath     = "_meta/fields.json"
	metaTimeLayout = "2006-01-02T15:04:05Z"
)

var issuePath = regexp.MustCompile(`^[A-Z][A-Z0-9_]*/[A-Z][A-Z0-9_]*-[1-9][0-9]*\.json$`)

// ProjectMeta records how a project was synced.
type ProjectMeta struct {
	JQL   string `json:"jql"`
	Count int    `json:"count"`
}

// Meta is _meta/snapshot.json. Times are stored in UTC to the second.
// SchemaVersion is the number found on the branch.
type Meta struct {
	FetchedAt     *time.Time // nil means never synced
	LastFullAt    *time.Time
	JiraURL       string
	Projects      map[string]ProjectMeta
	SchemaVersion int
	ToolVersion   string
	FieldsHash    string
}

type metaFile struct {
	FetchedAt     *string                `json:"fetched_at"`
	LastFullAt    *string                `json:"last_full_at"`
	JiraURL       string                 `json:"jira_url"`
	Projects      map[string]ProjectMeta `json:"projects"`
	SchemaVersion int                    `json:"schema_version"`
	ToolVersion   string                 `json:"tool_version"`
	FieldsHash    string                 `json:"fields_hash"`
}

// Snapshot is the whole cache branch.
type Snapshot struct {
	Meta   Meta
	Fields []Field // nil means no _meta/fields.json
	Issues map[string]Issue
}

// NewerSchemaError reports a snapshot written by a newer jira-git-cache.
type NewerSchemaError struct{ Got, Supported int }

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("snapshot schema_version %d is newer than this jira-git-cache supports (%d)", e.Got, e.Supported)
}

// ErrNotSnapshot means the files have no _meta/snapshot.json.
var ErrNotSnapshot = errors.New("not a jira-git-cache snapshot")

// Decode reads the branch's files. It ignores paths that are not snapshot files.
func Decode(files map[string][]byte) (Snapshot, error) {
	raw, ok := files[metaPath]
	if !ok {
		return Snapshot{}, ErrNotSnapshot
	}
	meta, err := decodeMeta(raw)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Meta: meta, Issues: map[string]Issue{}}
	if raw, ok := files[fieldsPath]; ok {
		if err := json.Unmarshal(raw, &s.Fields); err != nil {
			return Snapshot{}, fmt.Errorf("%s: %w", fieldsPath, err)
		}
	}
	for path, raw := range files {
		if !issuePath.MatchString(path) {
			continue
		}
		var iss Issue
		if err := json.Unmarshal(raw, &iss); err != nil {
			return Snapshot{}, fmt.Errorf("%s: %w", path, err)
		}
		if Path(iss.Key) != path {
			return Snapshot{}, fmt.Errorf("%s holds issue %q", path, iss.Key)
		}
		s.Issues[iss.Key] = iss
	}
	return s, nil
}

func decodeMeta(raw []byte) (Meta, error) {
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return Meta{}, fmt.Errorf("%s: %w", metaPath, err)
	}
	if version.SchemaVersion > SchemaVersion {
		return Meta{}, &NewerSchemaError{Got: version.SchemaVersion, Supported: SchemaVersion}
	}
	if version.SchemaVersion < 1 {
		return Meta{}, fmt.Errorf("%s: invalid schema_version %d", metaPath, version.SchemaVersion)
	}
	var f metaFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Meta{}, fmt.Errorf("%s: %w", metaPath, err)
	}
	fetchedAt, err := parseMetaTime(f.FetchedAt)
	if err != nil {
		return Meta{}, fmt.Errorf("%s: fetched_at: %w", metaPath, err)
	}
	lastFullAt, err := parseMetaTime(f.LastFullAt)
	if err != nil {
		return Meta{}, fmt.Errorf("%s: last_full_at: %w", metaPath, err)
	}
	if f.Projects == nil {
		f.Projects = map[string]ProjectMeta{}
	}
	return Meta{
		FetchedAt:     fetchedAt,
		LastFullAt:    lastFullAt,
		JiraURL:       f.JiraURL,
		Projects:      f.Projects,
		SchemaVersion: f.SchemaVersion,
		ToolVersion:   f.ToolVersion,
		FieldsHash:    f.FieldsHash,
	}, nil
}

func parseMetaTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(metaTimeLayout, *s)
	if err != nil {
		return nil, fmt.Errorf("parse time: %w", err)
	}
	if t.Format(metaTimeLayout) != *s {
		return nil, fmt.Errorf("time %q is not in UTC to the second", *s)
	}
	return &t, nil
}

func formatMetaTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(metaTimeLayout)
	return &s
}

// Encode returns the branch's files.
func (s Snapshot) Encode() map[string][]byte {
	m := s.Meta
	if m.Projects == nil {
		m.Projects = map[string]ProjectMeta{}
	}
	files := map[string][]byte{metaPath: encodeJSON(metaFile{
		FetchedAt:     formatMetaTime(m.FetchedAt),
		LastFullAt:    formatMetaTime(m.LastFullAt),
		JiraURL:       m.JiraURL,
		Projects:      m.Projects,
		SchemaVersion: m.SchemaVersion,
		ToolVersion:   m.ToolVersion,
		FieldsHash:    m.FieldsHash,
	})}
	if s.Fields != nil {
		files[fieldsPath] = encodeJSON(s.Fields)
	}
	for key, iss := range s.Issues {
		files[Path(key)] = EncodeIssue(iss)
	}
	return files
}

// EncodeIssue returns an issue's JSON file. Nil lists and Custom are written empty.
func EncodeIssue(iss Issue) []byte {
	iss.Labels = append([]string{}, iss.Labels...)
	iss.Links = append([]Link{}, iss.Links...)
	iss.Comments = append([]Comment{}, iss.Comments...)
	if iss.Custom == nil {
		iss.Custom = map[string]any{}
	}
	return encodeJSON(iss)
}

// encodeJSON writes every JSON file, so equal values give equal bytes.
func encodeJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("encode snapshot JSON: %v", err))
	}
	return buf.Bytes()
}
