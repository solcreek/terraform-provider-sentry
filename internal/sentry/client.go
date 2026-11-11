// SPDX-License-Identifier: MPL-2.0

// Package sentry is a minimal, dependency-free (stdlib only) client for the
// subset of the Sentry web API (https://docs.sentry.io/api/) that this
// Terraform provider needs: organizations, teams, projects, a project's team
// access and its client keys.
//
// It follows Sentry's published API reference and was checked against
// sentry.io (2026-11-11). Sentry's writes are synchronous. Deletes of teams
// and projects are only scheduled, but both stop answering (404) and free
// their slug at once.
package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is Sentry's SaaS. Self-hosted installs and the region
// hosts (https://us.sentry.io, https://de.sentry.io) are set with WithBaseURL.
const DefaultBaseURL = "https://sentry.io"

// apiPrefix is the path every API endpoint lives under.
const apiPrefix = "/api/0"

// maxConcurrent caps requests in flight. Sentry limits concurrent requests
// per caller as well as requests per window, and Terraform runs up to ten
// operations at once.
const maxConcurrent = 4

// maxNetworkRetries is how often a GET is repeated after a transport error
// (timeout, reset connection).
const maxNetworkRetries = 2

// maxRetryWait bounds how long one rate-limit back-off may sleep.
const maxRetryWait = 60 * time.Second

// Client is a Sentry API client. It is safe for concurrent use.
type Client struct {
	http       *http.Client
	baseURL    string
	token      string
	userAgent  string
	maxRetries int
	retryBase  time.Duration
	slots      chan struct{}
	now        func() time.Time
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the Sentry base URL, e.g. https://sentry.example.com.
// A trailing slash or /api/0 suffix is accepted and ignored.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			u = strings.TrimRight(u, "/")
			c.baseURL = strings.TrimSuffix(u, apiPrefix)
		}
	}
}

// WithTimeout sets the per-request HTTP timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.http.Timeout = d
		}
	}
}

// WithHTTPClient injects a custom *http.Client (mainly for tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// New builds a Client authenticated with a Sentry auth token (an internal
// integration token or a personal token).
func New(token string, opts ...Option) *Client {
	c := &Client{
		http:       &http.Client{Timeout: 60 * time.Second},
		baseURL:    DefaultBaseURL,
		token:      token,
		userAgent:  "terraform-provider-sentry",
		maxRetries: 5,
		retryBase:  time.Second,
		slots:      make(chan struct{}, maxConcurrent),
		now:        time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is returned for any non-2xx response.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("sentry API error %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sentry API error %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// StatusCode returns the HTTP status of an APIError, or 0.
func StatusCode(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode
	}
	return 0
}

// rawResponse is one HTTP exchange, before the status is interpreted.
type rawResponse struct {
	status int
	header http.Header
	body   []byte
}

// send performs a request with retries on rate limiting and returns the raw
// response whatever its status. path is relative to /api/0.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, body any) (*rawResponse, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		payload = b
	}

	u := c.baseURL + apiPrefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		// The slot is held only for the HTTP round trip, never while a
		// back-off sleeps.
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		resp, err := c.http.Do(req)
		var respBody []byte
		if err == nil {
			respBody, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
		<-c.slots
		if err != nil {
			// A read that died on the network is safe to repeat; a write is
			// not, since it may have landed.
			if method == http.MethodGet && ctx.Err() == nil && attempt < maxNetworkRetries {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(1<<attempt) * c.retryBase):
				}
				continue
			}
			return nil, fmt.Errorf("%s %s: %w", method, path, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryWait(resp.Header, attempt)):
			}
			continue
		}
		return &rawResponse{status: resp.StatusCode, header: resp.Header, body: respBody}, nil
	}
}

// retryWait is how long to back off after a 429: Retry-After when present,
// else until the rate-limit window resets (X-Sentry-Rate-Limit-Reset, UTC
// epoch seconds), else exponentially: 1s, 2s, 4s, 8s, 16s.
func (c *Client) retryWait(h http.Header, attempt int) time.Duration {
	wait := time.Duration(1<<attempt) * c.retryBase
	if secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && secs >= 0 {
		wait = time.Duration(secs) * time.Second
	} else if reset, err := strconv.ParseFloat(strings.TrimSpace(h.Get("X-Sentry-Rate-Limit-Reset")), 64); err == nil {
		if until := time.Unix(int64(reset), 0).Sub(c.now()); until > 0 {
			wait = until
		} else {
			wait = 0
		}
	}
	if wait > maxRetryWait {
		wait = maxRetryWait
	}
	return wait
}

// do performs a request and decodes a 2xx response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	raw, err := c.send(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	return decode(method, path, raw, out)
}

func decode(method, path string, raw *rawResponse, out any) error {
	if raw.status < 200 || raw.status > 299 {
		return parseError(raw.status, raw.body)
	}
	if out != nil && len(raw.body) > 0 {
		if err := json.Unmarshal(raw.body, out); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return nil
}

// list fetches every page of a list endpoint and appends the items to out,
// which must point to a slice.
func list[T any](ctx context.Context, c *Client, path string, query url.Values, out *[]T) error {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	for {
		raw, err := c.send(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return err
		}
		var page []T
		if err := decode(http.MethodGet, path, raw, &page); err != nil {
			return err
		}
		*out = append(*out, page...)
		cursor := nextCursor(raw.header.Get("Link"))
		if cursor == "" {
			return nil
		}
		q.Set("cursor", cursor)
	}
}

var (
	linkRel     = regexp.MustCompile(`rel="?([a-z]+)"?`)
	linkResults = regexp.MustCompile(`results="?([a-z]+)"?`)
	linkCursor  = regexp.MustCompile(`cursor="([^"]*)"`)
)

// nextCursor returns the cursor of the next page from a Link header, or ""
// on the last page. Sentry always sends a rel="next" link and marks whether
// it leads anywhere with results="true|false". Only the cursor is taken from
// the link, so the token is never sent to a host the link names.
func nextCursor(link string) string {
	for _, part := range strings.Split(link, ",") {
		rel := linkRel.FindStringSubmatch(part)
		results := linkResults.FindStringSubmatch(part)
		cursor := linkCursor.FindStringSubmatch(part)
		if rel == nil || results == nil || cursor == nil {
			continue
		}
		if rel[1] == "next" && results[1] == "true" {
			return cursor[1]
		}
	}
	return ""
}

func parseError(status int, body []byte) error {
	ae := &APIError{StatusCode: status, Body: string(body), Message: errorMessage(body)}
	// Make credential problems actionable.
	switch status {
	case http.StatusUnauthorized:
		ae.Message = strings.TrimSpace(ae.Message + " (the auth token is missing, invalid, expired or revoked — check SENTRY_AUTH_TOKEN)")
	case http.StatusForbidden:
		ae.Message = strings.TrimSpace(ae.Message + " (the auth token lacks a scope this call needs, or has no access to the organization, team or project)")
	}
	return ae
}

// errorMessage extracts a readable reason from a Sentry error body, which is
// either {"detail": "..."}, {"detail": {"message": "..."}} or a map of field
// names to lists of validation messages.
func errorMessage(body []byte) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return ""
	}
	if raw, ok := obj["detail"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var d struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &d) == nil && d.Message != "" {
			return d.Message
		}
	}
	var parts []string
	for field, raw := range obj {
		var msgs []string
		if json.Unmarshal(raw, &msgs) == nil && len(msgs) > 0 {
			parts = append(parts, field+": "+strings.Join(msgs, " "))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// seg escapes one path segment.
func seg(s string) string { return url.PathEscape(s) }
