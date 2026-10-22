// Package jira reads issues from the Jira Data Center REST API v2.
package jira

import (
	"encoding/json"
	"fmt"
	"time"
)

// TimeLayout is the format of Jira timestamps, such as 2026-10-20T09:15:40.123+0000.
const TimeLayout = "2006-01-02T15:04:05.000-0700"

// User is a Jira user.
type User struct {
	Name         string `json:"name"`
	Key          string `json:"key"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress,omitempty"`
	Active       bool   `json:"active"`
}

// ServerInfo is the response of /rest/api/2/serverInfo.
type ServerInfo struct {
	BaseURL        string `json:"baseUrl"`
	Version        string `json:"version"`
	VersionNumbers []int  `json:"versionNumbers"`
	DeploymentType string `json:"deploymentType"`
	ServerTitle    string `json:"serverTitle"`
}

// FieldSchema describes the type of a Field.
type FieldSchema struct {
	Type     string `json:"type"`
	Items    string `json:"items,omitempty"`
	System   string `json:"system,omitempty"`
	Custom   string `json:"custom,omitempty"`
	CustomID int    `json:"customId,omitempty"`
}

// Field is an entry of /rest/api/2/field.
type Field struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Custom bool        `json:"custom"`
	Schema FieldSchema `json:"schema"`
}

// Issue is an issue with its raw field values, keyed by field id.
type Issue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// Comment is an issue comment. Author is nil for an anonymous comment.
type Comment struct {
	ID      string `json:"id"`
	Author  *User  `json:"author"`
	Body    string `json:"body"`
	Created string `json:"created"`
	Updated string `json:"updated"`
}

// SearchPage is one page of /rest/api/2/search results.
type SearchPage struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	Issues     []Issue `json:"issues"`
}

// ParseTime parses a timestamp in TimeLayout.
func ParseTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(TimeLayout, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Jira time: %w", err)
	}
	return t, nil
}
