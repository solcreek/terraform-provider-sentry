// SPDX-License-Identifier: MPL-2.0

package sentry

import (
	"context"
	"errors"
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
	// TeamRoles is the member's role on each of those teams; Role is null
	// when the member has no more than their default team role.
	TeamRoles []TeamRole `json:"teamRoles"`
}

// TeamRole is a member's role on one team.
type TeamRole struct {
	TeamSlug string  `json:"teamSlug"`
	Role     *string `json:"role"`
}

// defaultTeamRole is the team role a member has when none is set.
const defaultTeamRole = "contributor"

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
//
// The update endpoint replaces the member's teams with the teamRoles it is
// sent, and an update without teamRoles takes the member off every team
// (observed on sentry.io, 2026-11-11). The member's current teams are
// therefore read and sent back unchanged.
func (c *Client) UpdateMemberRole(ctx context.Context, org, memberID, role string) (*Member, error) {
	current, err := c.GetMember(ctx, org, memberID)
	if err != nil {
		return nil, err
	}
	type teamRole struct {
		TeamSlug string `json:"teamSlug"`
		Role     string `json:"role"`
	}
	roles := make([]teamRole, 0, len(current.Teams))
	seen := map[string]bool{}
	for _, tr := range current.TeamRoles {
		r := defaultTeamRole
		if tr.Role != nil && *tr.Role != "" {
			r = *tr.Role
		}
		roles = append(roles, teamRole{TeamSlug: tr.TeamSlug, Role: r})
		seen[tr.TeamSlug] = true
	}
	for _, slug := range current.Teams {
		if !seen[slug] {
			roles = append(roles, teamRole{TeamSlug: slug, Role: defaultTeamRole})
		}
	}
	var out Member
	body := map[string]any{"orgRole": role, "teamRoles": roles}
	if err := c.do(ctx, http.MethodPut, membersPath(org)+seg(memberID)+"/", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveMember removes a member, or withdraws a pending invitation.
func (c *Client) RemoveMember(ctx context.Context, org, memberID string) error {
	return c.do(ctx, http.MethodDelete, membersPath(org)+seg(memberID)+"/", nil, nil, nil)
}

func memberTeamPath(org, memberID, team string) string {
	return membersPath(org) + seg(memberID) + "/teams/" + seg(team) + "/"
}

// ErrTeamAccessRequested is returned by AddTeamMember when Sentry did not add
// the member but filed a request to join the team instead (HTTP 202), which
// happens when the token may not add members to that team directly.
var ErrTeamAccessRequested = errors.New("sentry filed an access request instead of adding the member to the team")

// AddTeamMember puts an organization member on a team (ID or slug). It is
// not an error if the member is already on it.
func (c *Client) AddTeamMember(ctx context.Context, org, memberID, team string) error {
	raw, err := c.send(ctx, http.MethodPost, memberTeamPath(org, memberID, team), nil, nil)
	if err != nil {
		return err
	}
	if raw.status == http.StatusAccepted {
		return ErrTeamAccessRequested
	}
	return decode(http.MethodPost, "", raw, nil)
}

// RemoveTeamMember takes an organization member off a team (ID or slug).
func (c *Client) RemoveTeamMember(ctx context.Context, org, memberID, team string) error {
	return c.do(ctx, http.MethodDelete, memberTeamPath(org, memberID, team), nil, nil, nil)
}
