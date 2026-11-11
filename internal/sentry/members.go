// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"net/http"
	"strings"
)

// Member is a member of an organization: a user, or an invitation that has
// not been accepted yet (Pending).
type Member struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	OrgRole string `json:"orgRole"`
	Pending bool   `json:"pending"`
	// Teams is the slugs of the member's teams. Only the detail endpoint
	// (GetMember) reports it.
	Teams []string `json:"teams"`
}

// InTeam reports whether the member is on the team with this slug.
func (m *Member) InTeam(slug string) bool {
	for _, s := range m.Teams {
		if s == slug {
			return true
		}
	}
	return false
}

func membersPath(org string) string { return "/organizations/" + seg(org) + "/members/" }

// ListMembers returns every member of the organization, including pending
// invitations.
func (c *Client) ListMembers(ctx context.Context, org string) ([]Member, error) {
	var out []Member
	if err := list(ctx, c, membersPath(org), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindMemberByEmail returns the member with this email address
// (case-insensitive), or nil.
func (c *Client) FindMemberByEmail(ctx context.Context, org, email string) (*Member, error) {
	members, err := c.ListMembers(ctx, org)
	if err != nil {
		return nil, err
	}
	for i := range members {
		if strings.EqualFold(members[i].Email, email) {
			return &members[i], nil
		}
	}
	return nil, nil
}

// GetMember returns one member, with its teams.
func (c *Client) GetMember(ctx context.Context, org, memberID string) (*Member, error) {
	var out Member
	if err := c.do(ctx, http.MethodGet, membersPath(org)+seg(memberID)+"/", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InviteMember invites an email address to the organization with an
// organization role. Sentry emails the invitation.
func (c *Client) InviteMember(ctx context.Context, org, email, role string) (*Member, error) {
	var out Member
	body := map[string]any{"email": email, "orgRole": role}
	if err := c.do(ctx, http.MethodPost, membersPath(org), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMemberRole changes a member's organization role. Sentry only allows
// this with a personal (user) token, not an internal integration's.
func (c *Client) UpdateMemberRole(ctx context.Context, org, memberID, role string) (*Member, error) {
	var out Member
	if err := c.do(ctx, http.MethodPut, membersPath(org)+seg(memberID)+"/", nil, map[string]any{"orgRole": role}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveMember removes a member, or withdraws a pending invitation.
func (c *Client) RemoveMember(ctx context.Context, org, memberID string) error {
	return c.do(ctx, http.MethodDelete, membersPath(org)+seg(memberID)+"/", nil, nil, nil)
}
