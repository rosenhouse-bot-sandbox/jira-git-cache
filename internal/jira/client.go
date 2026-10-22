package jira

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	searchPageSize  = 1000
	commentPageSize = 1000
	maxEnumerations = 3
	maxRetryAfter   = time.Minute
	maxBackoff      = 30 * time.Second

	serverInfoPath = "/rest/api/2/serverInfo"
)

var (
	// ErrUnauthenticated means Jira served the request anonymously or answered 401.
	ErrUnauthenticated = errors.New("401 Unauthorized: Jira rejected the token (invalid or expired)")
	// ErrWindowShifted means every SearchAll attempt saw the result set change while paging.
	ErrWindowShifted = errors.New("search results kept shifting while paging")
)

// APIError is any non-2xx response that is not ErrUnauthenticated.
type APIError struct {
	Method, Path string // Path excludes the query.
	StatusCode   int
	Messages     []string
	FieldErrors  map[string]string
}

func (e *APIError) Error() string {
	msgs := slices.Clone(e.Messages)
	for _, k := range slices.Sorted(maps.Keys(e.FieldErrors)) {
		msgs = append(msgs, k+": "+e.FieldErrors[k])
	}
	if len(msgs) == 0 {
		msgs = []string{http.StatusText(e.StatusCode)}
	}
	return fmt.Sprintf("%s %s: %d: %s", e.Method, e.Path, e.StatusCode, strings.Join(msgs, "; "))
}

// Client calls the Jira REST API v2 with a personal access token.
type Client struct {
	BaseURL     string                                     // It may include a context path but no trailing slash.
	Token       string                                     // Surrounding whitespace is trimmed before use.
	HTTP        *http.Client                               // Nil means http.DefaultClient.
	Sleep       func(context.Context, time.Duration) error // Nil means a timer that returns ctx.Err() if ctx ends first.
	MaxAttempts int                                        // Zero means 5.
}

// Myself returns the token's user.
func (c *Client) Myself(ctx context.Context) (User, error) {
	var u User
	return u, c.get(ctx, "/rest/api/2/myself", nil, &u)
}

// ServerInfo returns the server's version and deployment type.
func (c *Client) ServerInfo(ctx context.Context) (ServerInfo, error) {
	var info ServerInfo
	return info, c.get(ctx, serverInfoPath, nil, &info)
}

// Fields returns every field the user can see.
func (c *Client) Fields(ctx context.Context) ([]Field, error) {
	var fs []Field
	return fs, c.get(ctx, "/rest/api/2/field", nil, &fs)
}

// SearchPage returns one page of a JQL search. A nil fields uses Jira's default.
func (c *Client) SearchPage(ctx context.Context, jql string, fields []string, startAt, maxResults int) (SearchPage, error) {
	q := url.Values{"jql": {jql}, "startAt": {strconv.Itoa(startAt)}, "maxResults": {strconv.Itoa(maxResults)}}
	setFields(q, fields)
	var page SearchPage
	return page, c.get(ctx, "/rest/api/2/search", q, &page)
}

// SearchAll returns every issue a JQL search matches, deduplicated by key in server order.
// It restarts when the result set changes while paging.
func (c *Client) SearchAll(ctx context.Context, jql string, fields []string) ([]Issue, error) {
	for range maxEnumerations {
		issues, complete, err := c.enumerate(ctx, jql, fields)
		if err != nil {
			return nil, err
		}
		if complete {
			return issues, nil
		}
	}
	return nil, fmt.Errorf("search %q: %w", jql, ErrWindowShifted)
}

func (c *Client) enumerate(ctx context.Context, jql string, fields []string) ([]Issue, bool, error) {
	page, err := c.SearchPage(ctx, jql, fields, 0, searchPageSize)
	if err != nil {
		return nil, false, err
	}
	total := page.Total
	issues := []Issue{}
	seen := map[string]bool{}
	for startAt := 0; ; {
		for _, iss := range page.Issues {
			if !seen[iss.Key] {
				seen[iss.Key] = true
				issues = append(issues, iss)
			}
		}
		startAt += len(page.Issues)
		done := startAt >= total
		if done || len(page.Issues) == 0 {
			return issues, done, nil
		}
		if page, err = c.SearchPage(ctx, jql, fields, startAt, searchPageSize); err != nil {
			return nil, false, err
		}
		if page.Total != total {
			return nil, false, nil
		}
	}
}

// GetIssue returns one issue. A nil fields returns every field.
func (c *Client) GetIssue(ctx context.Context, key string, fields []string) (Issue, error) {
	q := url.Values{}
	setFields(q, fields)
	var iss Issue
	return iss, c.get(ctx, "/rest/api/2/issue/"+url.PathEscape(key), q, &iss)
}

// Comments returns every comment on an issue, in server order.
func (c *Client) Comments(ctx context.Context, key string) ([]Comment, error) {
	var all []Comment
	for {
		var page struct {
			Total    int       `json:"total"`
			Comments []Comment `json:"comments"`
		}
		q := url.Values{"startAt": {strconv.Itoa(len(all))}, "maxResults": {strconv.Itoa(commentPageSize)}, "orderBy": {"created"}}
		if err := c.get(ctx, "/rest/api/2/issue/"+url.PathEscape(key)+"/comment", q, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Comments...)
		if len(all) >= page.Total || len(page.Comments) == 0 {
			return all, nil
		}
	}
}

func setFields(q url.Values, fields []string) {
	if fields != nil {
		q.Set("fields", strings.Join(fields, ","))
	}
}

// get decodes the JSON response of a GET into out, retrying transient failures.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	attempts := cmp.Or(c.MaxAttempts, 5)
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		retry, wait, err := c.try(ctx, path, q, out)
		if !retry || attempts <= 1 {
			return err
		}
		if attempt >= attempts {
			return fmt.Errorf("after %d attempts: %w", attempt, err)
		}
		if wait == 0 {
			wait = backoff
		}
		backoff = min(2*backoff, maxBackoff)
		if sleepErr := sleep(ctx, wait); sleepErr != nil {
			return fmt.Errorf("%w; stopped retrying: %w", err, sleepErr)
		}
	}
}

// try makes one request. On a retryable failure it reports retry, with the server's Retry-After or 0.
func (c *Client) try(ctx context.Context, path string, q url.Values, out any) (retry bool, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return false, 0, fmt.Errorf("GET %s: %w", path, err)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	req.Header.Set("Accept", "application/json")
	res, err := cmp.Or(c.HTTP, http.DefaultClient).Do(req)
	if err != nil {
		return transient(err), 0, fmt.Errorf("GET %s: %w", path, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized || path != serverInfoPath && res.Header.Get("X-AUSERNAME") == "anonymous" {
		return false, 0, fmt.Errorf("GET %s: %w", path, ErrUnauthenticated)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return transient(err), 0, fmt.Errorf("GET %s: read response: %w", path, err)
	}
	if res.StatusCode/100 != 2 {
		var e struct {
			ErrorMessages []string          `json:"errorMessages"`
			Errors        map[string]string `json:"errors"`
		}
		_ = json.Unmarshal(body, &e)
		retry := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		return retry, retryAfter(res.Header), &APIError{
			Method: http.MethodGet, Path: path, StatusCode: res.StatusCode,
			Messages: e.ErrorMessages, FieldErrors: e.Errors,
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, 0, fmt.Errorf("GET %s: response is not JSON (Content-Type %q): %w", path, res.Header.Get("Content-Type"), err)
	}
	return false, 0, nil
}

// retryAfter reads Retry-After as integer seconds, capped at maxRetryAfter. It returns 0 for anything else.
func retryAfter(h http.Header) time.Duration {
	secs, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0
	}
	return time.Duration(min(max(secs, 0), int(maxRetryAfter/time.Second))) * time.Second
}

func transient(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}
