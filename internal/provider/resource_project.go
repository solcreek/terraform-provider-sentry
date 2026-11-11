// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
)

type projectResource struct {
	data *providerData
}

func newProjectResource() resource.Resource { return &projectResource{} }

type projectModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Slug               types.String `tfsdk:"slug"`
	Platform           types.String `tfsdk:"platform"`
	TeamID             types.String `tfsdk:"team_id"`
	DefaultRules       types.Bool   `tfsdk:"default_rules"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
}

// fill copies what Sentry reports. team_id is kept while that team still has
// access and cleared when it lost it, so the next plan restores the access.
func (m *projectModel) fill(p *sentry.Project) {
	m.ID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	m.Slug = types.StringValue(p.Slug)
	m.Platform = stringOrNull(p.Platform)
	if !p.HasTeam(m.TeamID.ValueString()) {
		m.TeamID = types.StringNull()
	}
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project, owned by a team. The project is tracked by its numeric ID, so changing " +
			"`name` or `slug` renames it in place. Give further teams access with `sentry_project_team`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Project ID. Unlike the slug, it never changes; reference projects by it.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the project.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Slug that identifies the project in URLs. When unset, Sentry derives it " +
					"from the name at creation, and it then stays as it is when the name changes.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{stringvalidator.RegexMatches(slugPattern, slugPatternMessage)},
			},
			"platform": schema.StringAttribute{
				MarkdownDescription: "Platform of the project, e.g. `python-django` or `javascript-react`. " +
					"Removing it from the configuration leaves the platform as it is.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"team_id": schema.StringAttribute{
				MarkdownDescription: "ID of the team that owns the project, e.g. `sentry_team.example.id`. " +
					"Changing it gives the new team access and then revokes the old team's.",
				Required:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"default_rules": schema.BoolAttribute{
				MarkdownDescription: "Whether Sentry creates its default alert, which notifies on every new " +
					"issue, with the project. Sentry's default is `true`. Only applies at creation: Sentry " +
					"does not report it, so later changes have no effect.",
				Optional: true,
			},
			"deletion_protection": schema.BoolAttribute{
				MarkdownDescription: "Refuse to delete the project. Defaults to `true`, because deleting a " +
					"project deletes its events and Sentry has no undo. Apply `false` before destroying.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := sentry.CreateProjectInput{
		Name:     plan.Name.ValueString(),
		Slug:     plan.Slug.ValueString(),
		Platform: plan.Platform.ValueString(),
	}
	if !plan.DefaultRules.IsNull() {
		in.DefaultRules = plan.DefaultRules.ValueBoolPointer()
	}
	created, err := r.data.client.CreateProject(ctx, r.data.org, plan.TeamID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create project", err.Error())
		return
	}
	// The create response is not documented to carry every team; read back.
	project, err := r.data.client.GetProject(ctx, r.data.org, created.ID)
	if err != nil {
		// The project exists: record it so it is not orphaned.
		plan.ID = types.StringValue(created.ID)
		plan.Name = types.StringValue(created.Name)
		plan.Slug = types.StringValue(created.Slug)
		plan.Platform = stringOrNull(created.Platform)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError("Unable to read project after creating it", err.Error())
		return
	}
	plan.fill(project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.data.client.GetProject(ctx, r.data.org, state.ID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	state.fill(project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, org, id := r.data.client, r.data.org, state.ID.ValueString()

	var in sentry.UpdateProjectInput
	changed := false
	if !plan.Name.Equal(state.Name) {
		in.Name, changed = plan.Name.ValueStringPointer(), true
	}
	if !plan.Slug.Equal(state.Slug) {
		in.Slug, changed = plan.Slug.ValueStringPointer(), true
	}
	if !plan.Platform.Equal(state.Platform) && !plan.Platform.IsNull() {
		in.Platform, changed = plan.Platform.ValueStringPointer(), true
	}
	if changed {
		if _, err := client.UpdateProject(ctx, org, id, in); err != nil {
			resp.Diagnostics.AddError("Unable to update project", err.Error())
			return
		}
	}

	// Move ownership: grant the new team first so the project is never
	// without one, then revoke the team this resource put there before.
	newTeam, oldTeam := plan.TeamID.ValueString(), state.TeamID.ValueString()
	if newTeam != oldTeam {
		current, err := client.GetProject(ctx, org, id)
		if err != nil {
			resp.Diagnostics.AddError("Unable to read project", err.Error())
			return
		}
		if !current.HasTeam(newTeam) {
			if err := client.AddProjectTeam(ctx, org, id, newTeam); err != nil {
				resp.Diagnostics.AddError("Unable to give the team access to the project", err.Error())
				return
			}
		}
		if oldTeam != "" && current.HasTeam(oldTeam) {
			if err := client.RemoveProjectTeam(ctx, org, id, oldTeam); err != nil {
				resp.Diagnostics.AddError("Unable to revoke the previous team's access to the project", err.Error())
				return
			}
		}
	}

	project, err := client.GetProject(ctx, org, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project after updating it", err.Error())
		return
	}
	plan.fill(project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeletionProtection.ValueBool() {
		resp.Diagnostics.AddError("Project is protected from deletion",
			fmt.Sprintf("Deleting project %s (%s) deletes all of its events and cannot be undone. "+
				"Set `deletion_protection = false`, apply, and then destroy.", state.Slug.ValueString(), state.ID.ValueString()))
		return
	}
	// Sentry schedules the deletion; the project is hidden meanwhile.
	if err := r.data.client.DeleteProject(ctx, r.data.org, state.ID.ValueString()); err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete project", err.Error())
	}
}

// ImportState accepts a project ID or slug. The owning team cannot be told
// apart from other teams with access, so the first one Sentry lists is taken;
// a different `team_id` in the configuration then shows as a change.
func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, err := r.data.client.GetProject(ctx, r.data.org, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import project",
			fmt.Sprintf("Expected a project ID or slug, got %q: %s", req.ID, err.Error()))
		return
	}
	state := projectModel{
		TeamID:             types.StringNull(),
		DefaultRules:       types.BoolNull(),
		DeletionProtection: types.BoolValue(true),
	}
	if len(project.Teams) > 0 {
		state.TeamID = types.StringValue(project.Teams[0].ID)
	}
	state.fill(project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
