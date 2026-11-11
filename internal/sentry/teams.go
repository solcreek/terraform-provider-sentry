// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
)

// Team is a Sentry team. ID is stable; Slug changes when the team is renamed.
type Team struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// teamBody is the create and update payload. Empty fields are left out, so
// Sentry derives a slug from the name on create.
type teamBody struct {
	Slug string `json:"slug,omitempty"`
	Name string `json:"name,omitempty"`
}

// CreateTeam creates a team. With an empty slug, Sentry derives one.
func (c *Client) CreateTeam(ctx context.Context, org, name, slug string) (*Team, error) {
	var out Team
	if err := c.do(ctx, http.MethodPost, "/organizations/"+seg(org)+"/teams/", nil, teamBody{Slug: slug, Name: name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTeam returns a team by ID or slug.
func (c *Client) GetTeam(ctx context.Context, org, team string) (*Team, error) {
	var out Team
	if err := c.do(ctx, http.MethodGet, "/teams/"+seg(org)+"/"+seg(team)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateTeam sets a team's name and slug. Sentry requires the slug on every
// update.
func (c *Client) UpdateTeam(ctx context.Context, org, team, name, slug string) (*Team, error) {
	var out Team
	if err := c.do(ctx, http.MethodPut, "/teams/"+seg(org)+"/"+seg(team)+"/", nil, teamBody{Slug: slug, Name: name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTeam schedules a team for deletion. Sentry deletes asynchronously
// and releases the slug at once.
func (c *Client) DeleteTeam(ctx context.Context, org, team string) error {
	return c.do(ctx, http.MethodDelete, "/teams/"+seg(org)+"/"+seg(team)+"/", nil, nil, nil)
}
