// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

// Data scrubbing settings exist on both the organization and each project,
// with the same fields. sentry_organization_data_scrubbing and
// sentry_project_data_scrubbing share everything here but their target.

var (
	_ resource.Resource                = (*projectDataScrubbingResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectDataScrubbingResource)(nil)
	_ resource.ResourceWithImportState = (*projectDataScrubbingResource)(nil)
	_ resource.Resource                = (*organizationDataScrubbingResource)(nil)
	_ resource.ResourceWithConfigure   = (*organizationDataScrubbingResource)(nil)
	_ resource.ResourceWithImportState = (*organizationDataScrubbingResource)(nil)
)

// scrubbingModel is the settings both resources manage.
type scrubbingModel struct {
	DataScrubber         types.Bool `tfsdk:"data_scrubber"`
	DataScrubberDefaults types.Bool `tfsdk:"data_scrubber_defaults"`
	ScrubIPAddresses     types.Bool `tfsdk:"scrub_ip_addresses"`
	SensitiveFields      types.Set  `tfsdk:"sensitive_fields"`
	SafeFields           types.Set  `tfsdk:"safe_fields"`
}

// scrubbingAttributes returns the shared schema. Every setting is optional:
// unset ones keep whatever Sentry has.
func scrubbingAttributes(scope string) map[string]schema.Attribute {
	keepBool := []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
	keepSet := []planmodifier.Set{setplanmodifier.UseStateForUnknown()}
	const kept = " When unset, Sentry's current setting is kept."
	return map[string]schema.Attribute{
		"data_scrubber": schema.BoolAttribute{
			MarkdownDescription: "Whether server-side data scrubbing is on for " + scope + "." + kept,
			Optional:            true, Computed: true, PlanModifiers: keepBool,
		},
		"data_scrubber_defaults": schema.BoolAttribute{
			MarkdownDescription: "Whether Sentry's default scrubbers, for things like passwords and credit card " +
				"numbers, apply." + kept,
			Optional: true, Computed: true, PlanModifiers: keepBool,
		},
		"scrub_ip_addresses": schema.BoolAttribute{
			MarkdownDescription: "Whether client IP addresses are kept out of stored events." + kept,
			Optional:            true, Computed: true, PlanModifiers: keepBool,
		},
		"sensitive_fields": schema.SetAttribute{
			MarkdownDescription: "Additional field names to scrub. An empty set clears the list." + kept,
			Optional:            true, Computed: true, ElementType: types.StringType, PlanModifiers: keepSet,
		},
		"safe_fields": schema.SetAttribute{
			MarkdownDescription: "Field names the scrubbers leave alone. An empty set clears the list." + kept,
			Optional:            true, Computed: true, ElementType: types.StringType, PlanModifiers: keepSet,
		},
	}
}

func (m *scrubbingModel) fill(ctx context.Context, s *sentry.DataScrubbing) diag.Diagnostics {
	var diags, d diag.Diagnostics
	m.DataScrubber = types.BoolValue(s.DataScrubber)
	m.DataScrubberDefaults = types.BoolValue(s.DataScrubberDefaults)
	m.ScrubIPAddresses = types.BoolValue(s.ScrubIPAddresses)
	m.SensitiveFields, d = types.SetValueFrom(ctx, types.StringType, sortedCopy(s.SensitiveFields))
	diags.Append(d...)
	m.SafeFields, d = types.SetValueFrom(ctx, types.StringType, sortedCopy(s.SafeFields))
	diags.Append(d...)
	return diags
}

// input sends what the configuration sets; unset settings stay as they are.
func (m *scrubbingModel) input(ctx context.Context, diags *diag.Diagnostics) sentry.UpdateDataScrubbingInput {
	var in sentry.UpdateDataScrubbingInput
	known := func(v interface {
		IsNull() bool
		IsUnknown() bool
	}) bool {
		return !v.IsNull() && !v.IsUnknown()
	}
	if known(m.DataScrubber) {
		in.DataScrubber = m.DataScrubber.ValueBoolPointer()
	}
	if known(m.DataScrubberDefaults) {
		in.DataScrubberDefaults = m.DataScrubberDefaults.ValueBoolPointer()
	}
	if known(m.ScrubIPAddresses) {
		in.ScrubIPAddresses = m.ScrubIPAddresses.ValueBoolPointer()
	}
	if known(m.SensitiveFields) {
		fields := []string{}
		diags.Append(m.SensitiveFields.ElementsAs(ctx, &fields, false)...)
		in.SensitiveFields = &fields
	}
	if known(m.SafeFields) {
		fields := []string{}
		diags.Append(m.SafeFields.ElementsAs(ctx, &fields, false)...)
		in.SafeFields = &fields
	}
	return in
}

// checkStored reports settings Sentry stored differently from what was sent,
// instead of letting Terraform fail with an inconsistent result.
func checkStored(in sentry.UpdateDataScrubbingInput, got *sentry.DataScrubbing, diags *diag.Diagnostics) {
	differs := func(attr, sent, stored string) {
		diags.AddAttributeError(path.Root(attr), "Sentry stored a different value",
			fmt.Sprintf("Sent %s, but Sentry now reports %s. Sentry may normalize the value, or another setting may override it.", sent, stored))
	}
	boolean := func(attr string, sent *bool, stored bool) {
		if sent != nil && *sent != stored {
			differs(attr, fmt.Sprint(*sent), fmt.Sprint(stored))
		}
	}
	fields := func(attr string, sent *[]string, stored []string) {
		if sent == nil {
			return
		}
		a, b := strings.Join(sortedCopy(*sent), ", "), strings.Join(sortedCopy(stored), ", ")
		if a != b {
			differs(attr, "["+a+"]", "["+b+"]")
		}
	}
	boolean("data_scrubber", in.DataScrubber, got.DataScrubber)
	boolean("data_scrubber_defaults", in.DataScrubberDefaults, got.DataScrubberDefaults)
	boolean("scrub_ip_addresses", in.ScrubIPAddresses, got.ScrubIPAddresses)
	fields("sensitive_fields", in.SensitiveFields, got.SensitiveFields)
	fields("safe_fields", in.SafeFields, got.SafeFields)
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

const scrubbingDestroyNote = "Destroying this resource only stops managing the settings: they stay as they are, " +
	"since resetting them could switch scrubbing off."

// ------------------------------------------- sentry_project_data_scrubbing

type projectDataScrubbingResource struct {
	data *providerData
}

func newProjectDataScrubbingResource() resource.Resource { return &projectDataScrubbingResource{} }

type projectDataScrubbingModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	scrubbingModel
}

func (r *projectDataScrubbingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_data_scrubbing"
}

func (r *projectDataScrubbingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := scrubbingAttributes("the project")
	attrs["id"] = schema.StringAttribute{
		MarkdownDescription: "Same as `project_id`.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["project_id"] = schema.StringAttribute{
		MarkdownDescription: "ID of the project, e.g. `sentry_project.example.id`.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The server-side data scrubbing settings of a project: the scrubbers, and the " +
			"sensitive and safe field lists. Every project has them, so creating this resource takes them over. " +
			"Organization-wide settings (`sentry_organization_data_scrubbing`) apply on top. " + scrubbingDestroyNote,
		Attributes: attrs,
	}
}

func (r *projectDataScrubbingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectDataScrubbingResource) apply(ctx context.Context, plan *projectDataScrubbingModel, diags *diag.Diagnostics) {
	in := plan.input(ctx, diags)
	if diags.HasError() {
		return
	}
	got, err := r.data.client.UpdateProjectDataScrubbing(ctx, r.data.org, plan.ProjectID.ValueString(), in)
	if err != nil {
		diags.AddError("Unable to update the project's data scrubbing settings", err.Error())
		return
	}
	checkStored(in, got, diags)
	plan.ID = plan.ProjectID
	diags.Append(plan.fill(ctx, got)...)
}

func (r *projectDataScrubbingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectDataScrubbingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectDataScrubbingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectDataScrubbingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.client.GetProjectDataScrubbing(ctx, r.data.org, state.ProjectID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read the project's data scrubbing settings", err.Error())
		return
	}
	state.ID = state.ProjectID
	resp.Diagnostics.Append(state.fill(ctx, got)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectDataScrubbingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectDataScrubbingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete leaves the settings as they are; see scrubbingDestroyNote.
func (r *projectDataScrubbingResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState accepts a project ID or slug and settles on the ID.
func (r *projectDataScrubbingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, err := r.data.client.GetProject(ctx, r.data.org, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import the project's data scrubbing settings",
			fmt.Sprintf("Expected a project ID or slug, got %q: %s", req.ID, err.Error()))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), project.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project.ID)...)
}

// -------------------------------------- sentry_organization_data_scrubbing

type organizationDataScrubbingResource struct {
	data *providerData
}

func newOrganizationDataScrubbingResource() resource.Resource {
	return &organizationDataScrubbingResource{}
}

type organizationDataScrubbingModel struct {
	ID types.String `tfsdk:"id"`
	scrubbingModel
}

func (r *organizationDataScrubbingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_data_scrubbing"
}

func (r *organizationDataScrubbingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := scrubbingAttributes("every project of the organization")
	attrs["id"] = schema.StringAttribute{
		MarkdownDescription: "Organization ID.",
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organization-wide server-side data scrubbing settings, which apply to every " +
			"project on top of the project's own. There is one per organization, so creating this resource takes " +
			"it over; declare it once. " + scrubbingDestroyNote,
		Attributes: attrs,
	}
}

func (r *organizationDataScrubbingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

// organizationID resolves the configured organization, which may be a slug.
func (r *organizationDataScrubbingResource) organizationID(ctx context.Context, diags *diag.Diagnostics) types.String {
	org, err := r.data.client.GetOrganization(ctx, r.data.org)
	if err != nil {
		diags.AddError("Unable to read organization", err.Error())
		return types.StringNull()
	}
	return types.StringValue(org.ID)
}

func (r *organizationDataScrubbingResource) apply(ctx context.Context, plan *organizationDataScrubbingModel, diags *diag.Diagnostics) {
	in := plan.input(ctx, diags)
	if diags.HasError() {
		return
	}
	got, err := r.data.client.UpdateOrganizationDataScrubbing(ctx, r.data.org, in)
	if err != nil {
		diags.AddError("Unable to update the organization's data scrubbing settings", err.Error())
		return
	}
	checkStored(in, got, diags)
	diags.Append(plan.fill(ctx, got)...)
}

func (r *organizationDataScrubbingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationDataScrubbingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = r.organizationID(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *organizationDataScrubbingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state organizationDataScrubbingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.data.client.GetOrganizationDataScrubbing(ctx, r.data.org)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read the organization's data scrubbing settings", err.Error())
		return
	}
	state.ID = r.organizationID(ctx, &resp.Diagnostics)
	resp.Diagnostics.Append(state.fill(ctx, got)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *organizationDataScrubbingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan organizationDataScrubbingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete leaves the settings as they are; see scrubbingDestroyNote.
func (r *organizationDataScrubbingResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState ignores the given ID: there is one configuration, that of the
// provider's organization.
func (r *organizationDataScrubbingResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := r.organizationID(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
