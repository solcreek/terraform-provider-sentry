// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
)

// DataScrubbing is the server-side data scrubbing configuration that both
// organizations and projects have. It always exists; it can only be changed.
type DataScrubbing struct {
	// DataScrubber removes known sensitive values from events.
	DataScrubber bool `json:"dataScrubber"`
	// DataScrubberDefaults also scrubs Sentry's built-in list of sensitive
	// field names (passwords, credit cards and the like).
	DataScrubberDefaults bool `json:"dataScrubberDefaults"`
	// ScrubIPAddresses stops client IP addresses from being stored.
	ScrubIPAddresses bool `json:"scrubIPAddresses"`
	// SensitiveFields are additional field names to scrub.
	SensitiveFields []string `json:"sensitiveFields"`
	// SafeFields are field names the scrubbers leave alone.
	SafeFields []string `json:"safeFields"`
}

// UpdateDataScrubbingInput is a partial update: nil fields are left
// unchanged. A non-nil empty list clears the list.
type UpdateDataScrubbingInput struct {
	DataScrubber         *bool     `json:"dataScrubber,omitempty"`
	DataScrubberDefaults *bool     `json:"dataScrubberDefaults,omitempty"`
	ScrubIPAddresses     *bool     `json:"scrubIPAddresses,omitempty"`
	SensitiveFields      *[]string `json:"sensitiveFields,omitempty"`
	SafeFields           *[]string `json:"safeFields,omitempty"`
}

func projectPath(org, project string) string {
	return "/projects/" + seg(org) + "/" + seg(project) + "/"
}

// GetProjectDataScrubbing returns a project's data scrubbing settings.
func (c *Client) GetProjectDataScrubbing(ctx context.Context, org, project string) (*DataScrubbing, error) {
	var out DataScrubbing
	if err := c.do(ctx, http.MethodGet, projectPath(org, project), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProjectDataScrubbing changes the submitted data scrubbing settings
// of a project.
func (c *Client) UpdateProjectDataScrubbing(ctx context.Context, org, project string, in UpdateDataScrubbingInput) (*DataScrubbing, error) {
	var out DataScrubbing
	if err := c.do(ctx, http.MethodPut, projectPath(org, project), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetOrganizationDataScrubbing returns the organization-wide data scrubbing
// settings.
func (c *Client) GetOrganizationDataScrubbing(ctx context.Context, org string) (*DataScrubbing, error) {
	var out DataScrubbing
	if err := c.do(ctx, http.MethodGet, "/organizations/"+seg(org)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateOrganizationDataScrubbing changes the submitted organization-wide
// data scrubbing settings.
func (c *Client) UpdateOrganizationDataScrubbing(ctx context.Context, org string, in UpdateDataScrubbingInput) (*DataScrubbing, error) {
	var out DataScrubbing
	if err := c.do(ctx, http.MethodPut, "/organizations/"+seg(org)+"/", nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
