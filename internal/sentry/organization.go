// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
)

// Organization is the subset of an organization the provider reads.
type Organization struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// GetOrganization returns an organization by ID or slug.
func (c *Client) GetOrganization(ctx context.Context, org string) (*Organization, error) {
	var out Organization
	if err := c.do(ctx, http.MethodGet, "/organizations/"+seg(org)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
