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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                   = (*projectInboundFilterResource)(nil)
	_ resource.ResourceWithConfigure      = (*projectInboundFilterResource)(nil)
	_ resource.ResourceWithImportState    = (*projectInboundFilterResource)(nil)
	_ resource.ResourceWithValidateConfig = (*projectInboundFilterResource)(nil)
)

type projectInboundFilterResource struct {
	data *providerData
}

func newProjectInboundFilterResource() resource.Resource { return &projectInboundFilterResource{} }

type projectInboundFilterModel struct {
	ID         types.String `tfsdk:"id"`
	ProjectID  types.String `tfsdk:"project_id"`
	FilterID   types.String `tfsdk:"filter_id"`
	Active     types.Bool   `tfsdk:"active"`
	Subfilters types.Set    `tfsdk:"subfilters"`
}

func (m *projectInboundFilterModel) isLegacy() bool {
	return m.FilterID.ValueString() == sentry.LegacyBrowsersFilter
}

// fill copies the filter's state: the browser list for legacy-browsers, the
// switch for every other filter. The attribute that does not apply is null.
func (m *projectInboundFilterModel) fill(ctx context.Context, f *sentry.InboundFilter) diag.Diagnostics {
	m.ID = types.StringValue(m.ProjectID.ValueString() + "/" + f.ID)
	m.FilterID = types.StringValue(f.ID)
	if f.ID != sentry.LegacyBrowsersFilter {
		m.Active = types.BoolValue(f.Active)
		m.Subfilters = types.SetNull(types.StringType)
		return nil
	}
	m.Active = types.BoolNull()
	browsers := append([]string{}, f.Subfilters...)
	sort.Strings(browsers)
	var diags diag.Diagnostics
	m.Subfilters, diags = types.SetValueFrom(ctx, types.StringType, browsers)
	return diags
}

func (r *projectInboundFilterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_inbound_filter"
}

func (r *projectInboundFilterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One of a project's built-in inbound data filters, which drop events before they " +
			"count against the quota. Every project has all of them, so creating this resource takes the " +
			"filter over and destroying it turns the filter off.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<project_id>/<filter_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: projectIDDescription,
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"filter_id": schema.StringAttribute{
				MarkdownDescription: "The filter: `browser-extensions`, `localhost`, `web-crawlers`, " +
					"`filtered-transaction` (health check transactions) or `legacy-browsers`.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"active": schema.BoolAttribute{
				MarkdownDescription: "Whether the filter is on. Required for every filter except " +
					"`legacy-browsers`, which takes `subfilters` instead.",
				Optional: true,
			},
			"subfilters": schema.SetAttribute{
				MarkdownDescription: "Only for `legacy-browsers`, and required there: the browsers whose old " +
					"versions are filtered, e.g. `ie`, `edge`, `safari`, `firefox`, `chrome`, `opera`, " +
					"`android`, `opera_mini`. An empty set turns the filter off.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *projectInboundFilterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg projectInboundFilterModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.FilterID.IsUnknown() {
		return
	}
	if cfg.isLegacy() {
		if cfg.Subfilters.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("subfilters"), "Missing subfilters",
				"The `legacy-browsers` filter is configured with `subfilters`, the set of browsers to filter.")
		}
		if !cfg.Active.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("active"), "Unsupported attribute",
				"The `legacy-browsers` filter has no `active` switch; it is on when `subfilters` is not empty.")
		}
		return
	}
	if cfg.Active.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("active"), "Missing active",
			fmt.Sprintf("The `%s` filter is configured with `active`.", cfg.FilterID.ValueString()))
	}
	if !cfg.Subfilters.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("subfilters"), "Unsupported attribute",
			"Only the `legacy-browsers` filter has `subfilters`.")
	}
}

func (r *projectInboundFilterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

// apply writes the planned setting and reads the filter back, since the
// update answers without a body.
func (r *projectInboundFilterResource) apply(ctx context.Context, plan *projectInboundFilterModel, diags *diag.Diagnostics) {
	client, org := r.data.client, r.data.org
	projectID, filterID := plan.ProjectID.ValueString(), plan.FilterID.ValueString()

	existing, err := client.GetInboundFilter(ctx, org, projectID, filterID)
	if err != nil {
		diags.AddError("Unable to read inbound filters", err.Error())
		return
	}
	if existing == nil {
		diags.AddAttributeError(path.Root("filter_id"), "Unknown inbound filter",
			fmt.Sprintf("Project %s has no inbound filter %q.", projectID, filterID))
		return
	}

	var wanted []string
	if plan.isLegacy() {
		diags.Append(plan.Subfilters.ElementsAs(ctx, &wanted, false)...)
		if diags.HasError() {
			return
		}
		err = client.SetLegacyBrowsersFilter(ctx, org, projectID, wanted)
	} else {
		err = client.SetInboundFilter(ctx, org, projectID, filterID, plan.Active.ValueBool())
	}
	if err != nil {
		diags.AddError("Unable to update inbound filter", err.Error())
		return
	}

	got, err := client.GetInboundFilter(ctx, org, projectID, filterID)
	if err != nil || got == nil {
		diags.AddError("Unable to read the inbound filter after updating it", fmt.Sprint(err))
		return
	}
	// Sentry ignores browsers it does not know; say so rather than letting
	// the result differ from the plan.
	if plan.isLegacy() {
		sort.Strings(wanted)
		stored := append([]string{}, got.Subfilters...)
		sort.Strings(stored)
		if strings.Join(wanted, ",") != strings.Join(stored, ",") {
			diags.AddAttributeError(path.Root("subfilters"), "Sentry stored different subfilters",
				fmt.Sprintf("Sent [%s], but Sentry now reports [%s]. Sentry drops browser names it does not know.",
					strings.Join(wanted, ", "), strings.Join(stored, ", ")))
		}
	}
	diags.Append(plan.fill(ctx, got)...)
}

func (r *projectInboundFilterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectInboundFilterModel
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

func (r *projectInboundFilterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectInboundFilterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f, err := r.data.client.GetInboundFilter(ctx, r.data.org, state.ProjectID.ValueString(), state.FilterID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read inbound filters", err.Error())
		return
	}
	if f == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(state.fill(ctx, f)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectInboundFilterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectInboundFilterModel
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

// Delete turns the filter off; a project cannot be without it.
func (r *projectInboundFilterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectInboundFilterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, org, projectID := r.data.client, r.data.org, state.ProjectID.ValueString()
	var err error
	if state.isLegacy() {
		err = client.SetLegacyBrowsersFilter(ctx, org, projectID, nil)
	} else {
		err = client.SetInboundFilter(ctx, org, projectID, state.FilterID.ValueString(), false)
	}
	if err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to turn the inbound filter off", err.Error())
	}
}

func (r *projectInboundFilterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectRef, filterID, ok := strings.Cut(req.ID, "/")
	if !ok || projectRef == "" || filterID == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<project_id_or_slug>/<filter_id>`, got %q.", req.ID))
		return
	}
	project, err := r.data.client.GetProject(ctx, r.data.org, projectRef)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import inbound filter", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &projectInboundFilterModel{
		ID:         types.StringValue(project.ID + "/" + filterID),
		ProjectID:  types.StringValue(project.ID),
		FilterID:   types.StringValue(filterID),
		Active:     types.BoolNull(),
		Subfilters: types.SetNull(types.StringType),
	})...)
}
