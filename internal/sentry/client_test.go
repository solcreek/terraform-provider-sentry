// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("token", WithBaseURL(srv.URL))
	c.retryBase = time.Millisecond
	return c
}

func TestRequestShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-sentry" {
			t.Errorf("User-Agent = %q", got)
		}
		// Path segments are escaped, and every path ends in a slash.
		if got := r.URL.EscapedPath(); got != "/api/0/teams/acme/a%2Fb/" {
			t.Errorf("path = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"1","slug":"a-b","name":"A B"}`))
	})
	team, err := c.GetTeam(context.Background(), "acme", "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if team.ID != "1" || team.Slug != "a-b" {
		t.Fatalf("team = %+v", team)
	}
}

func TestBaseURLForms(t *testing.T) {
	for _, in := range []string{"https://sentry.example.com", "https://sentry.example.com/", "https://sentry.example.com/api/0/"} {
		if got := New("t", WithBaseURL(in)).baseURL; got != "https://sentry.example.com" {
			t.Errorf("WithBaseURL(%q) = %q", in, got)
		}
	}
	if got := New("t", WithBaseURL("")).baseURL; got != DefaultBaseURL {
		t.Errorf("default base URL = %q", got)
	}
}

func TestRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			// A window that has already reset: retry at once.
			w.Header().Set("X-Sentry-Rate-Limit-Reset", strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10))
			w.WriteHeader(http.StatusTooManyRequests)
		case 3:
			w.WriteHeader(http.StatusTooManyRequests) // no hint: exponential
		default:
			_, _ = w.Write([]byte(`{"id":"1","slug":"acme","name":"Acme"}`))
		}
	})
	org, err := c.GetOrganization(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if org.Slug != "acme" || calls.Load() != 4 {
		t.Fatalf("org %+v after %d calls", org, calls.Load())
	}
}

func TestRateLimitGivesUp(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"You are attempting to use this endpoint too frequently."}`))
	})
	c.maxRetries = 2
	_, err := c.GetOrganization(context.Background(), "acme")
	if StatusCode(err) != http.StatusTooManyRequests || calls.Load() != 3 {
		t.Fatalf("err = %v after %d calls", err, calls.Load())
	}
}

func TestRetryWait(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := New("t")
	c.now = func() time.Time { return now }
	header := func(k, v string) http.Header { return http.Header{k: []string{v}} }
	cases := []struct {
		name    string
		h       http.Header
		attempt int
		want    time.Duration
	}{
		{"retry-after", header("Retry-After", "7"), 0, 7 * time.Second},
		{"reset in 12s", header("X-Sentry-Rate-Limit-Reset", strconv.FormatInt(now.Unix()+12, 10)), 0, 12 * time.Second},
		{"reset as float", header("X-Sentry-Rate-Limit-Reset", "1700000003.0"), 0, 3 * time.Second},
		{"reset far away is capped", header("X-Sentry-Rate-Limit-Reset", strconv.FormatInt(now.Unix()+3600, 10)), 0, maxRetryWait},
		{"no hint, third attempt", http.Header{}, 2, 4 * time.Second},
	}
	for _, tc := range cases {
		if got := c.retryWait(tc.h, tc.attempt); got != tc.want {
			t.Errorf("%s: wait = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"detail":"Invalid token"}`, "Invalid token (the auth token is missing, invalid, expired or revoked"},
		{403, `{"detail":"You do not have permission to perform this action."}`, "lacks a scope"},
		{404, `{"detail":"The requested resource does not exist"}`, "sentry API error 404: The requested resource does not exist"},
		{400, `{"slug":["Too long.","Bad."],"name":["Required."]}`, "name: Required.; slug: Too long. Bad."},
		{400, `{"detail":{"message":"Nested reason","code":"x"}}`, "Nested reason"},
		{502, `<html>bad gateway</html>`, "sentry API error 502: <html>bad gateway</html>"},
	}
	for _, tc := range cases {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.GetOrganization(context.Background(), "acme")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: error = %v, want it to contain %q", tc.status, err, tc.want)
		}
		if StatusCode(err) != tc.status {
			t.Errorf("StatusCode = %d, want %d", StatusCode(err), tc.status)
		}
		if IsNotFound(err) != (tc.status == 404) {
			t.Errorf("%d: IsNotFound = %t", tc.status, IsNotFound(err))
		}
	}
}

func TestNextCursor(t *testing.T) {
	more := `<https://sentry.io/api/0/x/?&cursor=0:0:1>; rel="previous"; results="false"; cursor="0:0:1", ` +
		`<https://sentry.io/api/0/x/?&cursor=0:100:0>; rel="next"; results="true"; cursor="0:100:0"`
	last := strings.Replace(more, `rel="next"; results="true"`, `rel="next"; results="false"`, 1)
	for link, want := range map[string]string{more: "0:100:0", last: "", "": "", "garbage": ""} {
		if got := nextCursor(link); got != want {
			t.Errorf("nextCursor(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestListFollowsPages(t *testing.T) {
	var cursors []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		next, more, id := "0:1:0", true, "a"
		if cursor == "0:1:0" {
			next, more, id = "0:2:0", false, "b"
		}
		// The link names another host; only its cursor may be used.
		w.Header().Set("Link", fmt.Sprintf(`<https://elsewhere.invalid/?cursor=%s>; rel="next"; results="%t"; cursor="%s"`, next, more, next))
		_, _ = w.Write([]byte(`[{"id":"` + id + `","projectId":7,"dsn":{"public":"p"}}]`))
	})
	keys, err := c.ListProjectKeys(context.Background(), "acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != "a" || keys[1].ID != "b" || keys[1].ProjectIDString() != "7" {
		t.Fatalf("keys = %+v", keys)
	}
	if strings.Join(cursors, ",") != ",0:1:0" {
		t.Fatalf("cursors requested = %q", cursors)
	}
}

func TestUpdateKeySendsNullRateLimit(t *testing.T) {
	var bodies []map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{"id":"k"}`))
	})
	ctx := context.Background()
	if _, err := c.UpdateProjectKey(ctx, "acme", "api", "k", UpdateProjectKeyInput{Name: "n", IsActive: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateProjectKey(ctx, "acme", "api", "k", UpdateProjectKeyInput{Name: "n", IsActive: true, RateLimit: &RateLimit{Window: 60, Count: 5}}); err != nil {
		t.Fatal(err)
	}
	if v, present := bodies[0]["rateLimit"]; !present || v != nil {
		t.Errorf("rateLimit should be an explicit null, body = %v", bodies[0])
	}
	if bodies[0]["isActive"] != false {
		t.Errorf("isActive = false must be sent, body = %v", bodies[0])
	}
	if rl, _ := bodies[1]["rateLimit"].(map[string]any); rl["window"] != float64(60) || rl["count"] != float64(5) {
		t.Errorf("rateLimit = %v", bodies[1]["rateLimit"])
	}
}

func TestConcurrencyIsCapped(t *testing.T) {
	var inFlight, peak atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	done := make(chan error, 20)
	for range 20 {
		go func() {
			_, err := c.GetOrganization(context.Background(), "acme")
			done <- err
		}()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() > maxConcurrent {
		t.Fatalf("%d requests in flight, cap is %d", peak.Load(), maxConcurrent)
	}
}
