// Package jiramock is an in-process fake of the Jira Data Center 10.3 REST API, for tests.
package jiramock

import (
	"bytes"
	"encoding/json"
	"time"
)

// Layout is Jira's wire time format.
const Layout = "2006-01-02T15:04:05.000-0700"

// DefaultNow is the server's clock unless Server.Now is replaced.
var DefaultNow = time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)

// Issue is one stored issue. Fields and Comments hold raw Jira JSON values
// (what encoding/json would decode into any). Fields["updated"] must be a Layout
// string; Fields["resolution"] == nil means unresolved.
type Issue struct {
	ID       string
	Key      string
	Fields   map[string]any
	Comments []map[string]any // each has at least "id", "created" (Layout), "body"
}

type issueBody struct {
	Expand string         `json:"expand"`
	ID     string         `json:"id"`
	Self   string         `json:"self"`
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

func newIssueBody(i Issue, fields map[string]any) issueBody {
	return issueBody{
		Expand: "renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations",
		ID:     i.ID,
		Self:   exampleBase + "/rest/api/2/issue/" + i.ID,
		Key:    i.Key,
		Fields: fields,
	}
}

// JSON returns the body GET /rest/api/2/issue/{key} would return with all fields.
func (i Issue) JSON() []byte {
	return mustJSON(newIssueBody(i, i.Fields))
}

func mustJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
