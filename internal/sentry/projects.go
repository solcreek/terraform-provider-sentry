// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
)

// Project is the subset of a project the provider manages. ID is stable;
// Slug changes when the project is renamed.
type Project struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	Platform *string `json:"platform"`
	Teams    []Team  `json:"teams"`
	// Features lists the plan-dependent features the project has, e.g.
	// "rate-limits".
	Features []string `json:"features"`
}

// FeatureRateLimits is the project feature behind client key rate limits.
// Without it Sentry accepts a key's rateLimit and stores nothing: both key
// endpoints only read the field under features.has("projects:rate-limits").
const FeatureRateLimits = "rate-limits"

// HasFeature reports whether the project has a feature.
func (p *Project) HasFeature(name string) bool {
	for _, f := range p.Features {
		if f == name {
			return true
		}
	}
	return false
}

// HasTeam reports whether the team with this ID has access to the project.
func (p *Project) HasTeam(teamID string) bool {
	for _, t := range p.Teams {
		if t.ID == teamID {
			return true
		}
	}
	return false
}

// CreateProjectInput is the payload of CreateProject.
type CreateProjectInput struct {
	Name     string `json:"name"`
	Slug     string `json:"slug,omitempty"`
	Platform string `json:"platform,omitempty"`
	// DefaultRules is Sentry's switch for the alert it creates with every
	// new project; nil leaves Sentry's default (true).
	DefaultRules *bool `json:"default_rules,omitempty"`
}

// CreateProject creates a project owned by a team (ID or slug).
func (c *Client) CreateProject(ctx context.Context, org, team string, in CreateProjectInput) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPost, "/teams/"+seg(org)+"/"+seg(team)+"/projects/", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetProject returns a project by ID or slug, including its teams.
func (c *Client) GetProject(ctx context.Context, org, project string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodGet, "/projects/"+seg(org)+"/"+seg(project)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProjectInput is a partial update: nil fields are left unchanged.
// Sentry's schema lists some unrelated fields as required on this endpoint,
// but the live API accepts partial data, as its reference says (verified on
// sentry.io, 2026-11-11).
type UpdateProjectInput struct {
	Name     *string `json:"name,omitempty"`
	Slug     *string `json:"slug,omitempty"`
	Platform *string `json:"platform,omitempty"`
}

// UpdateProject changes a project's name, slug or platform.
func (c *Client) UpdateProject(ctx context.Context, org, project string, in UpdateProjectInput) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodPut, "/projects/"+seg(org)+"/"+seg(project)+"/", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject schedules a project, with all its events, for deletion.
// Sentry deletes asynchronously.
func (c *Client) DeleteProject(ctx context.Context, org, project string) error {
	return c.do(ctx, http.MethodDelete, "/projects/"+seg(org)+"/"+seg(project)+"/", nil, nil, nil)
}

// AddProjectTeam gives a team access to a project.
func (c *Client) AddProjectTeam(ctx context.Context, org, project, team string) error {
	return c.do(ctx, http.MethodPost, "/projects/"+seg(org)+"/"+seg(project)+"/teams/"+seg(team)+"/", nil, nil, nil)
}

// RemoveProjectTeam revokes a team's access to a project.
func (c *Client) RemoveProjectTeam(ctx context.Context, org, project, team string) error {
	return c.do(ctx, http.MethodDelete, "/projects/"+seg(org)+"/"+seg(project)+"/teams/"+seg(team)+"/", nil, nil, nil)
}
