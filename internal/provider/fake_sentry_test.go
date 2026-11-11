// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
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
// without credentials. It mirrors behaviour observed on sentry.io (verified
// 2026-11-11 with TestAccLifecycle and TestAccAPIFacts):
//
//   - organizations, teams and projects answer to their ID or their slug;
//     IDs are numeric strings, a key's projectId a number;
//   - a slug derived from a name that is taken gets a random suffix, while an
//     explicit slug that is taken is a 409 on create and a 400 on update;
//   - a new project has one team and a "Default" key; giving a team access
//     twice, revoking access it does not have and revoking the last team's
//     are all accepted;
//   - deleted teams and projects answer 404 at once and free their slug;
//   - a key's ID is its public key; on a plan without key rate limits the
//     rateLimit field is accepted and dropped (noRateLimits);
//   - ownership rules are stored without surrounding whitespace; the
//     legacy-browsers filter is addressed as "legacy-browsers" and reports
//     its browsers as a list, the other filters a boolean;
//   - a member update replaces the member's teams with the teamRoles sent;
//   - a project's data scrubbing settings accept partial updates and store
//     field names as written (the organization-wide ones follow the API
//     reference only: they were read, never written, on sentry.io);
//   - list endpoints send a Link header that always names a next cursor and
//     marks it results="false" on the last page;
//   - errors carry {"detail": "..."} or {"<field>": ["..."]}.
type fakeSentry struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	next     int
	teams    []*fakeTeam
	projects []*fakeProject
	members  []*fakeMember
	// orgScrubbing starts with everything off, as an organization does.
	orgScrubbing fakeScrubbing
	pageSize     int
	// closedTeams makes adding a member answer 202 (an access request), as
	// Sentry does when the token may not add members to a team directly.
	closedTeams bool
	// noRateLimits models a plan without key rate limits: projects lack the
	// "rate-limits" feature, and a key's rateLimit is accepted and dropped,
	// as observed on sentry.io (2026-11-11).
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
	Ownership      fakeOwnership
	Scrubbing      fakeScrubbing
	// Filters holds the switch filters that are on; LegacyBrowsers the
	// browsers the legacy-browsers filter drops.
	Filters        map[string]bool
	LegacyBrowsers []string
}

// fakeScrubbing is the data scrubbing settings of an organization or project.
type fakeScrubbing struct {
	DataScrubber, DataScrubberDefaults, ScrubIPAddresses bool
	SensitiveFields, SafeFields                          []string
}

// newProjectScrubbing is a new project's settings on sentry.io (observed
// 2026-11-11): both scrubbers on, IP addresses kept, no extra fields.
func newProjectScrubbing() fakeScrubbing {
	return fakeScrubbing{DataScrubber: true, DataScrubberDefaults: true}
}

func (s *fakeScrubbing) json(into map[string]any) {
	into["dataScrubber"], into["dataScrubberDefaults"], into["scrubIPAddresses"] = s.DataScrubber, s.DataScrubberDefaults, s.ScrubIPAddresses
	into["sensitiveFields"] = append([]string{}, s.SensitiveFields...)
	into["safeFields"] = append([]string{}, s.SafeFields...)
}

// update applies the submitted settings; missing ones are left alone.
func (s *fakeScrubbing) update(body map[string]any) {
	flag := func(key string, dst *bool) {
		if v, ok := body[key].(bool); ok {
			*dst = v
		}
	}
	fields := func(key string, dst *[]string) {
		raw, ok := body[key].([]any)
		if !ok {
			return
		}
		*dst = nil
		for _, v := range raw {
			if f, ok := v.(string); ok {
				*dst = append(*dst, f)
			}
		}
	}
	flag("dataScrubber", &s.DataScrubber)
	flag("dataScrubberDefaults", &s.DataScrubberDefaults)
	flag("scrubIPAddresses", &s.ScrubIPAddresses)
	fields("sensitiveFields", &s.SensitiveFields)
	fields("safeFields", &s.SafeFields)
}

// fakeOwnership starts out the way a new project's does on sentry.io.
type fakeOwnership struct {
	Raw                *string
	Fallthrough        bool
	AutoAssignment     string
	CodeownersAutoSync bool
}

func newFakeOwnership() fakeOwnership {
	return fakeOwnership{Fallthrough: true, AutoAssignment: "Auto Assign to Issue Owner", CodeownersAutoSync: true}
}

// fakeMember is an organization member; TeamIDs are the teams it is on.
type fakeMember struct {
	ID, Email, Role string
	Pending         bool
	TeamIDs         []string
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

// suffixed makes a derived slug unique the way Sentry does: with a short
// random-looking suffix.
func (f *fakeSentry) suffixed(slug string) string {
	f.next++
	return fmt.Sprintf("%s-%02x", slug, f.next%256)
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
	p := &fakeProject{ID: f.id(), Slug: slugify(name), Name: name, DefaultRules: true, Ownership: newFakeOwnership(), Scrubbing: newProjectScrubbing()}
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

// addMember inserts an active member, as if they had accepted an invitation.
func (f *fakeSentry) addMember(email, role string, teams ...*fakeTeam) *fakeMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &fakeMember{ID: f.id(), Email: email, Role: role}
	for _, t := range teams {
		m.TeamIDs = append(m.TeamIDs, t.ID)
	}
	f.members = append(f.members, m)
	return m
}

func (f *fakeSentry) member(id string) *fakeMember {
	for _, m := range f.members {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (f *fakeSentry) snapshotMember(id string) *fakeMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.member(id)
	if m == nil {
		return nil
	}
	cp := *m
	cp.TeamIDs = append([]string(nil), m.TeamIDs...)
	return &cp
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
	cp.LegacyBrowsers = append([]string(nil), p.LegacyBrowsers...)
	cp.Scrubbing.SensitiveFields = append([]string(nil), p.Scrubbing.SensitiveFields...)
	cp.Scrubbing.SafeFields = append([]string(nil), p.Scrubbing.SafeFields...)
	cp.Filters = map[string]bool{}
	for k, v := range p.Filters {
		cp.Filters[k] = v
	}
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
	features := []string{"servicehooks"}
	if !f.noRateLimits {
		features = append(features, "rate-limits")
	}
	out := map[string]any{"id": p.ID, "slug": p.Slug, "name": p.Name, "platform": p.Platform, "teams": teams, "features": features}
	p.Scrubbing.json(out)
	return out
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
	case route == "GET organizations/" || route == "PUT organizations/":
		if r.Method == http.MethodPut {
			f.orgScrubbing.update(body)
		}
		out := map[string]any{"id": fakeOrgID, "slug": fakeOrgSlug, "name": "Acme Inc"}
		f.orgScrubbing.json(out)
		writeJSON(w, 200, out)

	case parts[0] == "organizations" && len(parts) == 6 && parts[2] == "members" && parts[4] == "teams":
		f.serveMemberTeam(w, r.Method, arg(3), arg(5))

	case parts[0] == "organizations" && len(parts) >= 3 && len(parts) <= 4 && parts[2] == "members":
		memberID := ""
		if len(parts) == 4 {
			memberID = arg(3)
		}
		f.serveMembers(w, r, memberID, body)

	case route == "POST organizations/teams":
		f.createTeam(w, body)

	case parts[0] == "teams" && len(parts) == 3:
		f.serveTeam(w, r.Method, arg(2), body)

	case parts[0] == "teams" && len(parts) == 4 && parts[3] == "projects" && r.Method == http.MethodPost:
		f.createProject(w, arg(2), body)

	case parts[0] == "projects" && len(parts) == 3:
		f.serveProject(w, r.Method, arg(2), body)

	case parts[0] == "projects" && len(parts) >= 4 && len(parts) <= 5 && parts[3] == "filters":
		filterID := ""
		if len(parts) == 5 {
			filterID = arg(4)
		}
		f.serveFilters(w, r.Method, arg(2), filterID, body)

	case parts[0] == "projects" && len(parts) == 4 && parts[3] == "ownership":
		f.serveOwnership(w, r.Method, arg(2), body)

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
		if f.team(slug) != nil {
			slug = f.suffixed(slug)
		}
	} else if f.team(slug) != nil {
		detail(w, 409, "A team with this slug already exists.")
		return
	}
	if name == "" {
		name = slug
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
		for _, m := range f.members {
			m.TeamIDs = slices.DeleteFunc(m.TeamIDs, func(id string) bool { return id == t.ID })
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
		if f.project(slug) != nil {
			slug = f.suffixed(slug)
		}
	} else if f.project(slug) != nil {
		detail(w, 409, "A project with this slug already exists.")
		return
	}
	p := &fakeProject{ID: f.id(), Slug: slug, Name: name, TeamIDs: []string{t.ID}, DefaultRules: true, Ownership: newFakeOwnership(), Scrubbing: newProjectScrubbing()}
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
				writeJSON(w, 400, map[string]any{"slug": []string{"Another project (" + slug + ") is already using that slug"}})
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
		p.Scrubbing.update(body)
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

func (f *fakeSentry) serveOwnership(w http.ResponseWriter, method, projectRef string, body map[string]any) {
	p := f.project(projectRef)
	if p == nil {
		detail(w, http.StatusNotFound, "Project does not exist")
		return
	}
	o := &p.Ownership
	switch method {
	case http.MethodGet:
	case http.MethodPut:
		if raw, ok := body["raw"].(string); ok {
			if strings.Contains(raw, "not-a-rule") {
				writeJSON(w, 400, map[string]any{"raw": []string{"Parse error: 'ownership' (line 1, column 1)"}})
				return
			}
			// Sentry strips surrounding whitespace (observed 2026-11-11).
			raw = strings.TrimSpace(raw)
			o.Raw = &raw
			if raw == "" {
				o.Raw = nil
			}
		}
		if v, ok := body["fallthrough"].(bool); ok {
			o.Fallthrough = v
		}
		if v, ok := body["autoAssignment"].(string); ok {
			o.AutoAssignment = v
		}
		if v, ok := body["codeownersAutoSync"].(bool); ok {
			o.CodeownersAutoSync = v
		}
	default:
		w.WriteHeader(405)
		return
	}
	writeJSON(w, 200, map[string]any{
		"raw": o.Raw, "fallthrough": o.Fallthrough, "autoAssignment": o.AutoAssignment,
		"codeownersAutoSync": o.CodeownersAutoSync, "isActive": true, "schema": nil,
		"dateCreated": nil, "lastUpdated": nil,
	})
}

// The switch filters every project has, and the browsers legacy-browsers knows.
var (
	fakeSwitchFilters  = []string{"browser-extensions", "filtered-transaction", "localhost", "web-crawlers"}
	fakeLegacyBrowsers = []string{"ie", "edge", "safari", "firefox", "chrome", "opera", "android", "opera_mini"}
)

func (f *fakeSentry) serveFilters(w http.ResponseWriter, method, projectRef, filterID string, body map[string]any) {
	p := f.project(projectRef)
	if p == nil {
		detail(w, http.StatusNotFound, "Project does not exist")
		return
	}
	if filterID == "" {
		if method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		// Sorted by ID, legacy-browsers among the others, as Sentry lists them.
		out := []any{
			map[string]any{"id": "browser-extensions", "active": p.Filters["browser-extensions"]},
			map[string]any{"id": "filtered-transaction", "active": p.Filters["filtered-transaction"]},
			map[string]any{"id": "legacy-browsers", "active": append([]string{}, p.LegacyBrowsers...)},
			map[string]any{"id": "localhost", "active": p.Filters["localhost"]},
			map[string]any{"id": "web-crawlers", "active": p.Filters["web-crawlers"]},
		}
		writeJSON(w, 200, out)
		return
	}
	if method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	if filterID == "legacy-browsers" {
		p.LegacyBrowsers = nil
		raw, _ := body["subfilters"].([]any)
		for _, v := range raw {
			if s, ok := v.(string); ok && slices.Contains(fakeLegacyBrowsers, s) {
				p.LegacyBrowsers = append(p.LegacyBrowsers, s)
			}
		}
		w.WriteHeader(204)
		return
	}
	if !slices.Contains(fakeSwitchFilters, filterID) {
		notFound(w)
		return
	}
	if p.Filters == nil {
		p.Filters = map[string]bool{}
	}
	p.Filters[filterID], _ = body["active"].(bool)
	w.WriteHeader(204)
}

func (f *fakeSentry) memberJSON(m *fakeMember, withTeams bool) map[string]any {
	out := map[string]any{"id": m.ID, "email": m.Email, "name": m.Email, "orgRole": m.Role, "role": m.Role, "pending": m.Pending}
	if withTeams {
		teams, roles := []any{}, []any{}
		for _, id := range m.TeamIDs {
			if t := f.team(id); t != nil {
				teams = append(teams, t.Slug)
				roles = append(roles, map[string]any{"teamSlug": t.Slug, "role": nil})
			}
		}
		out["teams"], out["teamRoles"] = teams, roles
	}
	return out
}

// serveMembers models the member endpoints. Reads mirror sentry.io (verified
// read-only, 2026-11-11): the list has no teams, the detail does, emails are
// lowercase. Invitations, role changes and removals were verified with
// TestAccOrganizationMember the same day; the answer to a duplicate
// invitation follows the API reference only (the provider never sends one).
func (f *fakeSentry) serveMembers(w http.ResponseWriter, r *http.Request, memberID string, body map[string]any) {
	if memberID == "" {
		switch r.Method {
		case http.MethodGet:
			out := []any{}
			for _, m := range f.members {
				out = append(out, f.memberJSON(m, false))
			}
			writeJSON(w, 200, out)
		case http.MethodPost:
			email := strings.ToLower(str(body, "email"))
			if email == "" {
				writeJSON(w, 400, map[string]any{"email": []string{"This field is required."}})
				return
			}
			for _, m := range f.members {
				if m.Email == email {
					writeJSON(w, 400, map[string]any{"email": []string{"The user " + email + " has already been invited"}})
					return
				}
			}
			role := str(body, "orgRole")
			if role == "" {
				role = "member"
			}
			m := &fakeMember{ID: f.id(), Email: email, Role: role, Pending: true}
			f.members = append(f.members, m)
			writeJSON(w, 201, f.memberJSON(m, true))
		default:
			w.WriteHeader(405)
		}
		return
	}
	m := f.member(memberID)
	if m == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, f.memberJSON(m, true))
	case http.MethodPut:
		if role := str(body, "orgRole"); role != "" {
			m.Role = role
		}
		// The update replaces the member's teams with the teamRoles sent;
		// without teamRoles the member leaves every team (observed on
		// sentry.io, 2026-11-11).
		m.TeamIDs = nil
		if roles, ok := body["teamRoles"].([]any); ok {
			for _, raw := range roles {
				tr, _ := raw.(map[string]any)
				if t := f.team(str(tr, "teamSlug")); t != nil && str(tr, "role") != "" {
					m.TeamIDs = append(m.TeamIDs, t.ID)
				}
			}
		}
		writeJSON(w, 200, f.memberJSON(m, true))
	case http.MethodDelete:
		kept := f.members[:0]
		for _, o := range f.members {
			if o != m {
				kept = append(kept, o)
			}
		}
		f.members = kept
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

// serveMemberTeam models team membership. Adding and removing a member,
// pending ones included, was verified on sentry.io (2026-11-11). The 204 for
// a member already on the team and the 202 for an access request follow the
// API reference only.
func (f *fakeSentry) serveMemberTeam(w http.ResponseWriter, method, memberID, teamRef string) {
	m, t := f.member(memberID), f.team(teamRef)
	if m == nil || t == nil {
		notFound(w)
		return
	}
	on := slices.Contains(m.TeamIDs, t.ID)
	switch method {
	case http.MethodPost:
		switch {
		case on:
			w.WriteHeader(204)
		case f.closedTeams:
			w.WriteHeader(202)
		default:
			m.TeamIDs = append(m.TeamIDs, t.ID)
			writeJSON(w, 201, f.teamJSON(t))
		}
	case http.MethodDelete:
		m.TeamIDs = slices.DeleteFunc(m.TeamIDs, func(id string) bool { return id == t.ID })
		writeJSON(w, 200, f.teamJSON(t))
	default:
		w.WriteHeader(405)
	}
}
