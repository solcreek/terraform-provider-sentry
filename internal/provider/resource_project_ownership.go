// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                = (*projectOwnershipResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectOwnershipResource)(nil)
	_ resource.ResourceWithImportState = (*projectOwnershipResource)(nil)
)

// The auto-assignment settings, spelled as the Sentry API spells them.
var autoAssignmentValues = []string{
	"Auto Assign to Issue Owner",
	"Auto Assign to Suspect Commits",
	"Turn off Auto-Assignment",
}

type projectOwnershipResource struct {
	data *providerData
}

func newProjectOwnershipResource() resource.Resource { return &projectOwnershipResource{} }

type projectOwnershipModel struct {
	ID                 types.String `tfsdk:"id"`
	ProjectID          types.String `tfsdk:"project_id"`
	Raw                types.String `tfsdk:"raw"`
	Fallthrough        types.Bool   `tfsdk:"fallthrough"`
	AutoAssignment     types.String `tfsdk:"auto_assignment"`
	CodeownersAutoSync types.Bool   `tfsdk:"codeowners_auto_sync"`
}

func (m *projectOwnershipModel) fill(o *sentry.Ownership) {
	m.ID = m.ProjectID
	m.Raw = types.StringValue(o.RawText())
	m.Fallthrough = types.BoolValue(o.Fallthrough)
	m.AutoAssignment = types.StringValue(o.AutoAssignment)
	m.CodeownersAutoSync = types.BoolPointerValue(o.CodeownersAutoSync)
}

// input sends what the configuration sets; unset settings stay as they are.
func (m *projectOwnershipModel) input() sentry.UpdateOwnershipInput {
	in := sentry.UpdateOwnershipInput{Raw: m.Raw.ValueStringPointer()}
	if !m.Fallthrough.IsNull() && !m.Fallthrough.IsUnknown() {
		in.Fallthrough = m.Fallthrough.ValueBoolPointer()
	}
	if !m.AutoAssignment.IsNull() && !m.AutoAssignment.IsUnknown() {
		in.AutoAssignment = m.AutoAssignment.ValueStringPointer()
	}
	if !m.CodeownersAutoSync.IsNull() && !m.CodeownersAutoSync.IsUnknown() {
		in.CodeownersAutoSync = m.CodeownersAutoSync.ValueBoolPointer()
	}
	return in
}

func (r *projectOwnershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_ownership"
}

func (r *projectOwnershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The issue ownership rules of a project. Every project has exactly one ownership " +
			"configuration, so creating this resource takes it over and destroying it clears the rules, " +
			"leaving the other settings as they are.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `project_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "ID of the project, e.g. `sentry_project.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"raw": schema.StringAttribute{
				MarkdownDescription: "The ownership rules, in Sentry's " +
					"[ownership rule syntax](https://docs.sentry.io/product/issues/ownership-rules/), one per line, " +
					"e.g. `path:src/checkout/* #payments`. Sentry stores the text as given.",
				Required: true,
			},
			"fallthrough": schema.BoolAttribute{
				MarkdownDescription: "Whether issues that match no rule are assigned to all project members. " +
					"When unset, Sentry's current setting is kept.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"auto_assignment": schema.StringAttribute{
				MarkdownDescription: "Auto-assignment setting, spelled as the API spells it: " +
					"`Auto Assign to Issue Owner`, `Auto Assign to Suspect Commits` or `Turn off Auto-Assignment`. " +
					"When unset, Sentry's current setting is kept.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{stringvalidator.OneOf(autoAssignmentValues...)},
			},
			"codeowners_auto_sync": schema.BoolAttribute{
				MarkdownDescription: "Whether issue owners are synced when CODEOWNERS changes in a release. " +
					"When unset, Sentry's current setting is kept.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *projectOwnershipResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectOwnershipResource) apply(ctx context.Context, plan *projectOwnershipModel) error {
	o, err := r.data.client.UpdateOwnership(ctx, r.data.org, plan.ProjectID.ValueString(), plan.input())
	if err != nil {
		return err
	}
	wanted := plan.Raw.ValueString()
	plan.fill(o)
	if got := plan.Raw.ValueString(); got != wanted {
		return fmt.Errorf("the rules Sentry stored differ from the ones sent (%d bytes sent, %d stored); "+
			"write `raw` exactly as Sentry returns it", len(wanted), len(got))
	}
	return nil
}

func (r *projectOwnershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectOwnershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Unable to set project ownership", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectOwnershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectOwnershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	o, err := r.data.client.GetOwnership(ctx, r.data.org, state.ProjectID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read project ownership", err.Error())
		return
	}
	state.fill(o)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectOwnershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectOwnershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Unable to update project ownership", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete clears the rules; a project cannot be without an ownership
// configuration, and the other settings are left as they are.
func (r *projectOwnershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectOwnershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	empty := ""
	_, err := r.data.client.UpdateOwnership(ctx, r.data.org, state.ProjectID.ValueString(), sentry.UpdateOwnershipInput{Raw: &empty})
	if err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to clear project ownership rules", err.Error())
	}
}

// ImportState accepts a project ID or slug and settles on the ID.
func (r *projectOwnershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, err := r.data.client.GetProject(ctx, r.data.org, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import project ownership",
			fmt.Sprintf("Expected a project ID or slug, got %q: %s", req.ID, err.Error()))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), project.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project.ID)...)
}
