// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
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
	_ resource.Resource                = (*projectTeamResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectTeamResource)(nil)
	_ resource.ResourceWithImportState = (*projectTeamResource)(nil)
)

type projectTeamResource struct {
	data *providerData
}

func newProjectTeamResource() resource.Resource { return &projectTeamResource{} }

type projectTeamModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	TeamID    types.String `tfsdk:"team_id"`
}

func (r *projectTeamResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_team"
}

func (r *projectTeamResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives one more team access to a project. The team a project is created under is " +
			"managed by `sentry_project.team_id`; do not declare that one here as well.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<project_id>/<team_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: projectIDDescription,
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "ID of the team, e.g. `sentry_team.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (r *projectTeamResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectTeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectTeamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, teamID := plan.ProjectID.ValueString(), plan.TeamID.ValueString()
	project, err := r.data.client.GetProject(ctx, r.data.org, projectID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	if findTeam(project, teamID) != nil {
		resp.Diagnostics.AddError("Team already has access to the project",
			fmt.Sprintf("Team %s already has access to project %s. If it is the project's `team_id`, drop this "+
				"resource; otherwise import it: terraform import <address> %s/%s", teamID, projectID, projectID, teamID))
		return
	}
	if err := r.data.client.AddProjectTeam(ctx, r.data.org, projectID, teamID); err != nil {
		resp.Diagnostics.AddError("Unable to give the team access to the project", err.Error())
		return
	}
	plan.ID = types.StringValue(projectID + "/" + teamID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectTeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectTeamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.data.client.GetProject(ctx, r.data.org, state.ProjectID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	// The project and team stay as configured, which may be by slug.
	if findTeam(project, state.TeamID.ValueString()) == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called: every configurable attribute requires replacement.
func (r *projectTeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectTeamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectTeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectTeamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A 404 means the project or the team is already gone, and the access with it.
	err := r.data.client.RemoveProjectTeam(ctx, r.data.org, state.ProjectID.ValueString(), state.TeamID.ValueString())
	if err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to revoke the team's access to the project", err.Error())
	}
}

func (r *projectTeamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, team, ok := strings.Cut(req.ID, "/")
	if !ok || project == "" || team == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<project_id_or_slug>/<team_id_or_slug>`, got %q.", req.ID))
		return
	}
	// An import may name the project and team by slug; settle on IDs.
	p, err := r.data.client.GetProject(ctx, r.data.org, project)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import the team's access to the project", err.Error())
		return
	}
	t := findTeam(p, team)
	if t == nil {
		resp.Diagnostics.AddError("Unable to import the team's access to the project",
			fmt.Sprintf("Team %s has no access to project %s.", team, project))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &projectTeamModel{
		ID:        types.StringValue(p.ID + "/" + t.ID),
		ProjectID: types.StringValue(p.ID),
		TeamID:    types.StringValue(t.ID),
	})...)
}
