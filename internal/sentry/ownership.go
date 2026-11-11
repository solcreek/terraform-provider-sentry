// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
)

// Ownership is a project's issue ownership configuration. Every project has
// one; it cannot be created or deleted, only changed.
type Ownership struct {
	// Raw is the ownership rules as text; null in the API when there are none.
	Raw                *string `json:"raw"`
	Fallthrough        bool    `json:"fallthrough"`
	AutoAssignment     string  `json:"autoAssignment"`
	CodeownersAutoSync *bool   `json:"codeownersAutoSync"`
}

// RawText is the rules text, empty when there are no rules.
func (o *Ownership) RawText() string {
	if o.Raw == nil {
		return ""
	}
	return *o.Raw
}

// UpdateOwnershipInput is a partial update: nil fields are left unchanged.
type UpdateOwnershipInput struct {
	Raw                *string `json:"raw,omitempty"`
	Fallthrough        *bool   `json:"fallthrough,omitempty"`
	AutoAssignment     *string `json:"autoAssignment,omitempty"`
	CodeownersAutoSync *bool   `json:"codeownersAutoSync,omitempty"`
}

func ownershipPath(org, project string) string {
	return "/projects/" + seg(org) + "/" + seg(project) + "/ownership/"
}

// GetOwnership returns a project's ownership configuration.
func (c *Client) GetOwnership(ctx context.Context, org, project string) (*Ownership, error) {
	var out Ownership
	if err := c.do(ctx, http.MethodGet, ownershipPath(org, project), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateOwnership changes the submitted parts of a project's ownership
// configuration.
func (c *Client) UpdateOwnership(ctx context.Context, org, project string, in UpdateOwnershipInput) (*Ownership, error) {
	var out Ownership
	if err := c.do(ctx, http.MethodPut, ownershipPath(org, project), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
