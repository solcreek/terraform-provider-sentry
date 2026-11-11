// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// RateLimit caps the events a client key accepts: Count per Window seconds.
type RateLimit struct {
	Window int64 `json:"window"`
	Count  int64 `json:"count"`
}

// ProjectKey is a project's client key. Its ID is its public key.
type ProjectKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Public    string     `json:"public"`
	Secret    string     `json:"secret"`
	ProjectID int64      `json:"projectId"`
	IsActive  bool       `json:"isActive"`
	RateLimit *RateLimit `json:"rateLimit"`
	// DSN maps endpoint names ("public", "secret", "csp", "otlp_traces", …)
	// to URLs. The "secret" entry embeds the secret key.
	DSN map[string]string `json:"dsn"`
}

// ProjectIDString is the owning project's ID in the form other endpoints use.
func (k *ProjectKey) ProjectIDString() string { return strconv.FormatInt(k.ProjectID, 10) }

func keysPath(org, project string) string {
	return "/projects/" + seg(org) + "/" + seg(project) + "/keys/"
}

// ListProjectKeys returns every client key of a project.
func (c *Client) ListProjectKeys(ctx context.Context, org, project string) ([]ProjectKey, error) {
	var out []ProjectKey
	if err := list(ctx, c, keysPath(org, project), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateProjectKey creates a client key; Sentry generates its secrets.
func (c *Client) CreateProjectKey(ctx context.Context, org, project, name string, limit *RateLimit) (*ProjectKey, error) {
	body := map[string]any{"name": name}
	if limit != nil {
		body["rateLimit"] = limit
	}
	var out ProjectKey
	if err := c.do(ctx, http.MethodPost, keysPath(org, project), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetProjectKey returns one client key.
func (c *Client) GetProjectKey(ctx context.Context, org, project, keyID string) (*ProjectKey, error) {
	var out ProjectKey
	if err := c.do(ctx, http.MethodGet, keysPath(org, project)+seg(keyID)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProjectKeyInput sets a key's full managed configuration. A nil
// RateLimit removes the limit (Sentry takes an explicit null for that).
type UpdateProjectKeyInput struct {
	Name      string
	IsActive  bool
	RateLimit *RateLimit
}

// MarshalJSON always sends rateLimit, as null when there is none.
func (in UpdateProjectKeyInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string     `json:"name"`
		IsActive  bool       `json:"isActive"`
		RateLimit *RateLimit `json:"rateLimit"`
	}{in.Name, in.IsActive, in.RateLimit})
}

// UpdateProjectKey updates a client key.
func (c *Client) UpdateProjectKey(ctx context.Context, org, project, keyID string, in UpdateProjectKeyInput) (*ProjectKey, error) {
	var out ProjectKey
	if err := c.do(ctx, http.MethodPut, keysPath(org, project)+seg(keyID)+"/", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProjectKey deletes a client key. SDKs configured with its DSN stop
// reporting.
func (c *Client) DeleteProjectKey(ctx context.Context, org, project, keyID string) error {
	return c.do(ctx, http.MethodDelete, keysPath(org, project)+seg(keyID)+"/", nil, nil, nil)
}
