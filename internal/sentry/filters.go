// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// LegacyBrowsersFilter is the one inbound filter that is not a plain switch:
// it holds the list of legacy browsers it filters.
const LegacyBrowsersFilter = "legacy-browsers"

// InboundFilter is one of a project's built-in inbound data filters. A
// project always has all of them; they can only be turned on and off.
type InboundFilter struct {
	ID string
	// Active is whether the filter is on. For the legacy-browsers filter it
	// is on when at least one browser is filtered.
	Active bool
	// Subfilters is the browsers the legacy-browsers filter drops; nil for
	// the other filters.
	Subfilters []string
}

// UnmarshalJSON reads `active`, which is a boolean, or for legacy-browsers a
// list of browsers (or false when none is filtered).
func (f *InboundFilter) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID     string          `json:"id"`
		Active json.RawMessage `json:"active"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	f.ID, f.Active, f.Subfilters = raw.ID, false, nil
	var on bool
	if json.Unmarshal(raw.Active, &on) == nil {
		f.Active = on
		if raw.ID == LegacyBrowsersFilter {
			f.Subfilters = []string{}
		}
		return nil
	}
	if err := json.Unmarshal(raw.Active, &f.Subfilters); err != nil {
		return fmt.Errorf("inbound filter %q: active is neither a boolean nor a list: %s", raw.ID, raw.Active)
	}
	if f.Subfilters == nil {
		f.Subfilters = []string{}
	}
	f.Active = len(f.Subfilters) > 0
	return nil
}

func filtersPath(org, project string) string {
	return "/projects/" + seg(org) + "/" + seg(project) + "/filters/"
}

// ListInboundFilters returns every inbound filter of a project.
func (c *Client) ListInboundFilters(ctx context.Context, org, project string) ([]InboundFilter, error) {
	var out []InboundFilter
	if err := list(ctx, c, filtersPath(org, project), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetInboundFilter returns one inbound filter, or nil if the project has no
// filter with this ID.
func (c *Client) GetInboundFilter(ctx context.Context, org, project, id string) (*InboundFilter, error) {
	filters, err := c.ListInboundFilters(ctx, org, project)
	if err != nil {
		return nil, err
	}
	for i := range filters {
		if filters[i].ID == id {
			return &filters[i], nil
		}
	}
	return nil, nil
}

// SetInboundFilter turns a filter on or off.
func (c *Client) SetInboundFilter(ctx context.Context, org, project, id string, active bool) error {
	return c.do(ctx, http.MethodPut, filtersPath(org, project)+seg(id)+"/", nil, map[string]any{"active": active}, nil)
}

// SetLegacyBrowsersFilter sets the browsers the legacy-browsers filter drops;
// browsers left out are no longer filtered.
func (c *Client) SetLegacyBrowsersFilter(ctx context.Context, org, project string, browsers []string) error {
	if browsers == nil {
		browsers = []string{}
	}
	return c.do(ctx, http.MethodPut, filtersPath(org, project)+LegacyBrowsersFilter+"/", nil, map[string]any{"subfilters": browsers}, nil)
}
