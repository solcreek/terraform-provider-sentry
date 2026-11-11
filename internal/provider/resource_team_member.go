// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                = (*teamMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*teamMemberResource)(nil)
	_ resource.ResourceWithImportState = (*teamMemberResource)(nil)
)

type teamMemberResource struct {
	data *providerData
}

func newTeamMemberResource() resource.Resource { return &teamMemberResource{} }

type teamMemberModel struct {
	ID       types.String `tfsdk:"id"`
	TeamID   types.String `tfsdk:"team_id"`
	MemberID types.String `tfsdk:"member_id"`
}

func (r *teamMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member"
}

func (r *teamMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Puts an organization member on a team. The member keeps the team role Sentry " +
			"gives them by default; team roles are not managed yet.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<team_id>/<member_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "ID of the team, e.g. `sentry_team.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"member_id": schema.StringAttribute{
				MarkdownDescription: "Organization member ID, e.g. `sentry_organization_member.example.id` or " +
					"`data.sentry_organization_member.example.id`. It is not the user's ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (r *teamMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

// lookup resolves the team (ID or slug) and reports whether the member is on
// it. Sentry lists a member's teams by slug, so the team is read first.
func (r *teamMemberResource) lookup(ctx context.Context, teamRef, memberID string) (team *sentry.Team, onTeam bool, err error) {
	team, err = r.data.client.GetTeam(ctx, r.data.org, teamRef)
	if err != nil {
		return nil, false, err
	}
	member, err := r.data.client.GetMember(ctx, r.data.org, memberID)
	if err != nil {
		return nil, false, err
	}
	return team, member.InTeam(team.Slug), nil
}

func (r *teamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan teamMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	teamID, memberID := plan.TeamID.ValueString(), plan.MemberID.ValueString()
	_, onTeam, err := r.lookup(ctx, teamID, memberID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read the team or the member", err.Error())
		return
	}
	if onTeam {
		resp.Diagnostics.AddError("Member is already on the team",
			fmt.Sprintf("Member %s is already on team %s. Import it instead: terraform import <address> %s/%s", memberID, teamID, teamID, memberID))
		return
	}
	if err := r.data.client.AddTeamMember(ctx, r.data.org, memberID, teamID); err != nil {
		if errors.Is(err, sentry.ErrTeamAccessRequested) {
			resp.Diagnostics.AddError("Sentry did not add the member to the team",
				"Sentry filed a request to join the team instead of adding the member, because the auth token may "+
					"not add members to this team directly. Approve or discard the request in Sentry, and use a token "+
					"with the `team:write` and `org:write` scopes (or enable Open Membership).")
			return
		}
		resp.Diagnostics.AddError("Unable to add the member to the team", err.Error())
		return
	}
	plan.ID = types.StringValue(teamID + "/" + memberID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state teamMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	team, onTeam, err := r.lookup(ctx, state.TeamID.ValueString(), state.MemberID.ValueString())
	if err != nil {
		// The team or the member is gone, and the membership with it.
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read the team or the member", err.Error())
		return
	}
	if !onTeam {
		resp.State.RemoveResource(ctx)
		return
	}
	// An import may name the team by slug; settle on the ID.
	state.TeamID = types.StringValue(team.ID)
	state.ID = types.StringValue(team.ID + "/" + state.MemberID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called: every configurable attribute requires replacement.
func (r *teamMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan teamMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state teamMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.data.client.RemoveTeamMember(ctx, r.data.org, state.MemberID.ValueString(), state.TeamID.ValueString())
	if err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to remove the member from the team", err.Error())
	}
}

func (r *teamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	team, memberID, ok := strings.Cut(req.ID, "/")
	if !ok || team == "" || memberID == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<team_id_or_slug>/<member_id>`, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &teamMemberModel{
		ID:       types.StringValue(req.ID),
		TeamID:   types.StringValue(team),
		MemberID: types.StringValue(memberID),
	})...)
}
