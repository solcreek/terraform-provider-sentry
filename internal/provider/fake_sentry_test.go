// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	fakeOrgSlug = "acme"
	fakeOrgID   = "4500000000000001"
	fakeToken   = "test"
)

// fakeSentry is an in-memory stand-in for the parts of the Sentry API the
// provider uses, so every resource's Terraform lifecycle can be tested
// without credentials.
//
// Reads mirror the live API (verified read-only against sentry.io,
// 2026-11-11): organizations, teams and projects answer to their ID or their
// slug; IDs are numeric strings, a key's projectId a number; a key's ID is
// its public key and a project has a "Default" key; list endpoints send a
// Link header that always names a next cursor and marks it results="false"
// on the last page; errors carry {"detail": "..."} ("Invalid token" on 401,
// "Project does not exist" for a missing project).
//
// Writes follow Sentry's published API reference and OpenAPI schema only;
// they have NOT been checked against the live service, so passing lifecycle
// tests says nothing about live write behaviour (status codes of duplicate
// slugs, partial updates, scheduled deletes).
type fakeSentry struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	next     int
	teams    []*fakeTeam
	projects []*fakeProject
	pageSize int
	// noRateLimits models a plan without key rate limits: the field is
	// accepted and dropped, as observed on sentry.io (2026-11-11).
	noRateLimits bool
}

type fakeTeam struct {
	ID, Slug, Name string
}

type fakeProject struct {
	ID, Slug, Name string
	Platform       *string
	DefaultRules   bool
	TeamIDs        []string
	Keys           []*fakeKey
}

type fakeKey struct {
	ID, Name, Secret string
	IsActive         bool
	RateLimit        map[string]any
}

func newFakeSentry(t *testing.T) *fakeSentry {
	t.Helper()
	for _, k := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_ORG", "SENTRY_BASE_URL"} {
		t.Setenv(k, "")
	}
	f := &fakeSentry{t: t, pageSize: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSentry) providerConfig() string {
	return requiredProviders + fmt.Sprintf(`
provider "sentry" {
  auth_token   = %q
  organization = %q
  base_url     = %q
}
`, fakeToken, fakeOrgSlug, f.srv.URL)
}

func (f *fakeSentry) id() string {
	f.next++
	return strconv.Itoa(4500000000001000 + f.next)
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ------------------------------------------------------------ test helpers

// addTeam inserts a team, as if made in the Sentry UI.
func (f *fakeSentry) addTeam(name string) *fakeTeam {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTeam{ID: f.id(), Slug: slugify(name), Name: name}
	f.teams = append(f.teams, t)
	return t
}

// addProject inserts a project with its Default key, as if made in the UI.
func (f *fakeSentry) addProject(name string, teams ...*fakeTeam) *fakeProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &fakeProject{ID: f.id(), Slug: slugify(name), Name: name, DefaultRules: true}
	for _, t := range teams {
		p.TeamIDs = append(p.TeamIDs, t.ID)
	}
	p.Keys = []*fakeKey{f.newKey("Default")}
	f.projects = append(f.projects, p)
	return p
}

func (f *fakeSentry) newKey(name string) *fakeKey {
	id := f.id()
	return &fakeKey{ID: "pub" + id, Secret: "sec" + id, Name: name, IsActive: true}
}

func (f *fakeSentry) team(idOrSlug string) *fakeTeam {
	for _, t := range f.teams {
		if t.ID == idOrSlug || t.Slug == idOrSlug {
			return t
		}
	}
	return nil
}

func (f *fakeSentry) project(idOrSlug string) *fakeProject {
	for _, p := range f.projects {
		if p.ID == idOrSlug || p.Slug == idOrSlug {
			return p
		}
	}
	return nil
}

// snapshotProject returns a copy of a project for assertions, or nil.
func (f *fakeSentry) snapshotProject(idOrSlug string) *fakeProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.project(idOrSlug)
	if p == nil {
		return nil
	}
	cp := *p
	cp.TeamIDs = append([]string(nil), p.TeamIDs...)
	cp.Keys = nil
	for _, k := range p.Keys {
		kc := *k
		cp.Keys = append(cp.Keys, &kc)
	}
	return &cp
}

func (f *fakeSentry) snapshotTeam(idOrSlug string) *fakeTeam {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.team(idOrSlug)
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

// mutate changes the fake's state out of band, as a person in the UI would.
func (f *fakeSentry) mutate(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeSentry) removeProjectTeam(p *fakeProject, teamID string) {
	kept := p.TeamIDs[:0]
	for _, id := range p.TeamIDs {
		if id != teamID {
			kept = append(kept, id)
		}
	}
	p.TeamIDs = kept
}

// ------------------------------------------------------------------ server

func (f *fakeSentry) teamJSON(t *fakeTeam) map[string]any {
	return map[string]any{"id": t.ID, "slug": t.Slug, "name": t.Name}
}

func (f *fakeSentry) projectJSON(p *fakeProject) map[string]any {
	teams := []any{}
	for _, id := range p.TeamIDs {
		if t := f.team(id); t != nil {
			teams = append(teams, f.teamJSON(t))
		}
	}
	return map[string]any{"id": p.ID, "slug": p.Slug, "name": p.Name, "platform": p.Platform, "teams": teams}
}

func (f *fakeSentry) keyJSON(p *fakeProject, k *fakeKey) map[string]any {
	pid, _ := strconv.ParseInt(p.ID, 10, 64)
	host := "o1.ingest.sentry.test"
	var limit any
	if k.RateLimit != nil {
		limit = k.RateLimit
	}
	return map[string]any{
		"id": k.ID, "name": k.Name, "label": k.Name, "public": k.ID, "secret": k.Secret,
		"projectId": pid, "isActive": k.IsActive, "rateLimit": limit,
		"dsn": map[string]any{
			"public":      fmt.Sprintf("https://%s@%s/%s", k.ID, host, p.ID),
			"secret":      fmt.Sprintf("https://%s:%s@%s/%s", k.ID, k.Secret, host, p.ID),
			"csp":         fmt.Sprintf("https://%s/api/%s/csp-report/?sentry_key=%s", host, p.ID, k.ID),
			"otlp_traces": fmt.Sprintf("https://%s/api/%s/integration/otlp/v1/traces", host, p.ID),
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func detail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"detail": msg})
}

func notFound(w http.ResponseWriter) {
	detail(w, http.StatusNotFound, "The requested resource does not exist")
}

func (f *fakeSentry) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path, ok := strings.CutPrefix(r.URL.Path, "/api/0/")
	if !ok {
		notFound(w)
		return
	}

	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		detail(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	// Sentry's API paths all end in a slash.
	if !strings.HasSuffix(path, "/") {
		notFound(w)
		return
	}
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) < 2 || (parts[1] != fakeOrgSlug && parts[1] != fakeOrgID) {
		notFound(w)
		return
	}
	route := r.Method + " " + parts[0] + "/" + strings.Join(parts[2:], "/")
	arg := func(i int) string { return parts[i] }

	switch {
	case route == "GET organizations/":
		writeJSON(w, 200, map[string]any{"id": fakeOrgID, "slug": fakeOrgSlug, "name": "Acme Inc"})

	case route == "POST organizations/teams":
		f.createTeam(w, body)

	case parts[0] == "teams" && len(parts) == 3:
		f.serveTeam(w, r.Method, arg(2), body)

	case parts[0] == "teams" && len(parts) == 4 && parts[3] == "projects" && r.Method == http.MethodPost:
		f.createProject(w, arg(2), body)

	case parts[0] == "projects" && len(parts) == 3:
		f.serveProject(w, r.Method, arg(2), body)

	case parts[0] == "projects" && len(parts) == 5 && parts[3] == "teams":
		f.serveProjectTeam(w, r.Method, arg(2), arg(4))

	case parts[0] == "projects" && len(parts) >= 4 && len(parts) <= 5 && parts[3] == "keys":
		keyID := ""
		if len(parts) == 5 {
			keyID = arg(4)
		}
		f.serveKeys(w, r, arg(2), keyID, body)

	default:
		notFound(w)
	}
}

func str(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

func (f *fakeSentry) createTeam(w http.ResponseWriter, body map[string]any) {
	name, slug := str(body, "name"), str(body, "slug")
	if name == "" && slug == "" {
		detail(w, 400, "Name or slug is required")
		return
	}
	if slug == "" {
		slug = slugify(name)
	}
	if name == "" {
		name = slug
	}
	if f.team(slug) != nil {
		detail(w, 409, "A team with this slug already exists.")
		return
	}
	t := &fakeTeam{ID: f.id(), Slug: slug, Name: name}
	f.teams = append(f.teams, t)
	writeJSON(w, 201, f.teamJSON(t))
}

func (f *fakeSentry) serveTeam(w http.ResponseWriter, method, ref string, body map[string]any) {
	t := f.team(ref)
	if t == nil {
		notFound(w)
		return
	}
	switch method {
	case http.MethodGet:
		writeJSON(w, 200, f.teamJSON(t))
	case http.MethodPut:
		slug := str(body, "slug")
		if slug == "" {
			writeJSON(w, 400, map[string]any{"slug": []string{"This field is required."}})
			return
		}
		if other := f.team(slug); other != nil && other != t {
			writeJSON(w, 400, map[string]any{"slug": []string{"The slug \"" + slug + "\" is already in use."}})
			return
		}
		t.Slug = slug
		if name := str(body, "name"); name != "" {
			t.Name = name
		}
		writeJSON(w, 200, f.teamJSON(t))
	case http.MethodDelete:
		kept := f.teams[:0]
		for _, o := range f.teams {
			if o != t {
				kept = append(kept, o)
			}
		}
		f.teams = kept
		for _, p := range f.projects {
			f.removeProjectTeam(p, t.ID)
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func (f *fakeSentry) createProject(w http.ResponseWriter, teamRef string, body map[string]any) {
	t := f.team(teamRef)
	if t == nil {
		detail(w, 404, "Team not found.")
		return
	}
	name := str(body, "name")
	if name == "" {
		writeJSON(w, 400, map[string]any{"name": []string{"This field is required."}})
		return
	}
	slug := str(body, "slug")
	if slug == "" {
		slug = slugify(name)
	}
	if f.project(slug) != nil {
		detail(w, 409, "A project with this slug already exists.")
		return
	}
	p := &fakeProject{ID: f.id(), Slug: slug, Name: name, TeamIDs: []string{t.ID}, DefaultRules: true}
	if platform := str(body, "platform"); platform != "" {
		p.Platform = &platform
	}
	if v, ok := body["default_rules"].(bool); ok {
		p.DefaultRules = v
	}
	p.Keys = []*fakeKey{f.newKey("Default")}
	f.projects = append(f.projects, p)
	writeJSON(w, 201, f.projectJSON(p))
}

func (f *fakeSentry) serveProject(w http.ResponseWriter, method, ref string, body map[string]any) {
	p := f.project(ref)
	if p == nil {
		detail(w, http.StatusNotFound, "Project does not exist")
		return
	}
	switch method {
	case http.MethodGet:
		writeJSON(w, 200, f.projectJSON(p))
	case http.MethodPut:
		if slug := str(body, "slug"); slug != "" {
			if other := f.project(slug); other != nil && other != p {
				writeJSON(w, 400, map[string]any{"slug": []string{"Another project is already using that slug"}})
				return
			}
			p.Slug = slug
		}
		if name := str(body, "name"); name != "" {
			p.Name = name
		}
		if platform := str(body, "platform"); platform != "" {
			p.Platform = &platform
		}
		writeJSON(w, 200, f.projectJSON(p))
	case http.MethodDelete:
		kept := f.projects[:0]
		for _, o := range f.projects {
			if o != p {
				kept = append(kept, o)
			}
		}
		f.projects = kept
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func (f *fakeSentry) serveProjectTeam(w http.ResponseWriter, method, projectRef, teamRef string) {
	p, t := f.project(projectRef), f.team(teamRef)
	if p == nil || t == nil {
		notFound(w)
		return
	}
	switch method {
	case http.MethodPost:
		f.removeProjectTeam(p, t.ID)
		p.TeamIDs = append(p.TeamIDs, t.ID)
		writeJSON(w, 201, f.projectJSON(p))
	case http.MethodDelete:
		f.removeProjectTeam(p, t.ID)
		writeJSON(w, 200, f.projectJSON(p))
	default:
		w.WriteHeader(405)
	}
}

func (f *fakeSentry) serveKeys(w http.ResponseWriter, r *http.Request, projectRef, keyID string, body map[string]any) {
	p := f.project(projectRef)
	if p == nil {
		notFound(w)
		return
	}
	if keyID == "" {
		switch r.Method {
		case http.MethodGet:
			f.listKeys(w, r, p)
		case http.MethodPost:
			k := f.newKey(str(body, "name"))
			if k.Name == "" {
				k.Name = "Generated Name"
			}
			if rl, ok := body["rateLimit"].(map[string]any); ok && !f.noRateLimits {
				k.RateLimit = rl
			}
			p.Keys = append(p.Keys, k)
			writeJSON(w, 201, f.keyJSON(p, k))
		default:
			w.WriteHeader(405)
		}
		return
	}

	var key *fakeKey
	for _, k := range p.Keys {
		if k.ID == keyID {
			key = k
		}
	}
	if key == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, f.keyJSON(p, key))
	case http.MethodPut:
		if name := str(body, "name"); name != "" {
			key.Name = name
		}
		if v, ok := body["isActive"].(bool); ok {
			key.IsActive = v
		}
		if raw, present := body["rateLimit"]; present {
			key.RateLimit, _ = raw.(map[string]any) // an explicit null removes it
			if f.noRateLimits {
				key.RateLimit = nil
			}
		}
		writeJSON(w, 200, f.keyJSON(p, key))
	case http.MethodDelete:
		kept := p.Keys[:0]
		for _, k := range p.Keys {
			if k != key {
				kept = append(kept, k)
			}
		}
		p.Keys = kept
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

// listKeys pages through a project's keys the way Sentry does: a Link header
// that always names a next cursor and says whether it has results.
func (f *fakeSentry) listKeys(w http.ResponseWriter, r *http.Request, p *fakeProject) {
	offset := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		if cursor := strings.Split(c, ":"); len(cursor) == 3 {
			offset, _ = strconv.Atoi(cursor[1])
		}
	}
	end := min(offset+f.pageSize, len(p.Keys))
	offset = min(offset, end)
	page := []any{}
	for _, k := range p.Keys[offset:end] {
		page = append(page, f.keyJSON(p, k))
	}
	base := f.srv.URL + r.URL.Path
	w.Header().Set("Link", fmt.Sprintf(
		`<%s?&cursor=0:%d:1>; rel="previous"; results="%t"; cursor="0:%d:1", <%s?&cursor=0:%d:0>; rel="next"; results="%t"; cursor="0:%d:0"`,
		base, offset, offset > 0, offset, base, end, end < len(p.Keys), end))
	writeJSON(w, 200, page)
}
