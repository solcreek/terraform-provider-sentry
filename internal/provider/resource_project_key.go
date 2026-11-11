// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                = (*projectKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectKeyResource)(nil)
	_ resource.ResourceWithImportState = (*projectKeyResource)(nil)
)

type projectKeyResource struct {
	data *providerData
}

func newProjectKeyResource() resource.Resource { return &projectKeyResource{} }

type rateLimitModel struct {
	Window types.Int64 `tfsdk:"window"`
	Count  types.Int64 `tfsdk:"count"`
}

type projectKeyModel struct {
	ID        types.String    `tfsdk:"id"`
	ProjectID types.String    `tfsdk:"project_id"`
	Name      types.String    `tfsdk:"name"`
	IsActive  types.Bool      `tfsdk:"is_active"`
	RateLimit *rateLimitModel `tfsdk:"rate_limit"`
	PublicKey types.String    `tfsdk:"public_key"`
	SecretKey types.String    `tfsdk:"secret_key"`
	DSNPublic types.String    `tfsdk:"dsn_public"`
	DSNSecret types.String    `tfsdk:"dsn_secret"`
	Endpoints types.Map       `tfsdk:"endpoints"`
}

func (m *projectKeyModel) fill(ctx context.Context, k *sentry.ProjectKey) diag.Diagnostics {
	m.ID = types.StringValue(k.ID)
	m.ProjectID = types.StringValue(k.ProjectIDString())
	m.Name = types.StringValue(k.Name)
	m.IsActive = types.BoolValue(k.IsActive)
	m.RateLimit = nil
	if k.RateLimit != nil {
		m.RateLimit = &rateLimitModel{Window: types.Int64Value(k.RateLimit.Window), Count: types.Int64Value(k.RateLimit.Count)}
	}
	m.PublicKey = types.StringValue(k.Public)
	m.SecretKey = types.StringValue(k.Secret)
	m.DSNPublic = types.StringValue(k.DSN["public"])
	m.DSNSecret = types.StringValue(k.DSN["secret"])
	var diags diag.Diagnostics
	m.Endpoints, diags = keyEndpoints(ctx, k)
	return diags
}

func (m *projectKeyModel) rateLimit() *sentry.RateLimit {
	if m.RateLimit == nil {
		return nil
	}
	return &sentry.RateLimit{Window: m.RateLimit.Window.ValueInt64(), Count: m.RateLimit.Count.ValueInt64()}
}

// rateLimitIgnored reports that a rate limit was sent and Sentry dropped it.
// Sentry accepts the field and answers 2xx on plans without key rate limits
// (the project lacks the "rate-limits" feature), but stores nothing
// (observed on sentry.io, 2026-11-11).
func rateLimitIgnored(want *sentry.RateLimit, got *sentry.ProjectKey) bool {
	return want != nil && got.RateLimit == nil
}

const rateLimitIgnoredSummary = "Sentry ignored the rate limit"

const rateLimitIgnoredDetail = "Sentry accepted the request but did not store `rate_limit`. Client key rate limits " +
	"are not part of every Sentry plan; without them the API drops the setting silently. Remove `rate_limit` " +
	"from the configuration, or upgrade the organization's plan."

func (r *projectKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_key"
}

func (r *projectKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A client key of a project: the credentials behind a DSN. Sentry generates the " +
			"keys; `secret_key` and `dsn_secret` are sensitive but, like every attribute, are stored in the state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Client key ID, which is also its public key.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "ID of the project the key belongs to, e.g. `sentry_project.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the client key.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"is_active": schema.BoolAttribute{
				MarkdownDescription: "Whether the key accepts events. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"rate_limit": schema.SingleNestedAttribute{
				MarkdownDescription: "Cap on the events the key accepts. Without it the key is not rate limited. " +
					"Not every Sentry plan has key rate limits; where it is missing, Sentry drops the setting " +
					"and the provider reports an error.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"window": schema.Int64Attribute{
						MarkdownDescription: "Length of the window in seconds.",
						Required:            true,
						Validators:          []validator.Int64{int64validator.AtLeast(1)},
					},
					"count": schema.Int64Attribute{
						MarkdownDescription: "Number of events accepted per window.",
						Required:            true,
						Validators:          []validator.Int64{int64validator.AtLeast(1)},
					},
				},
			},
			"public_key": schema.StringAttribute{
				MarkdownDescription: "Public key.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"secret_key": schema.StringAttribute{
				MarkdownDescription: "Secret key. Only legacy SDKs need it.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keep,
			},
			"dsn_public": schema.StringAttribute{
				MarkdownDescription: "The DSN to configure SDKs with.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"dsn_secret": schema.StringAttribute{
				MarkdownDescription: "Legacy DSN that embeds the secret key.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keep,
			},
			"endpoints": schema.MapAttribute{
				MarkdownDescription: "The key's other ingestion endpoints by name, as Sentry reports them, " +
					"e.g. `csp`, `security`, `minidump`, `crons`, `otlp_traces` and `otlp_logs`.",
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *projectKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := plan.ProjectID.ValueString()
	key, err := r.data.client.CreateProjectKey(ctx, r.data.org, projectID, plan.Name.ValueString(), plan.rateLimit())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create client key", err.Error())
		return
	}
	// A key cannot be created inactive; deactivate it afterwards.
	if !plan.IsActive.ValueBool() {
		updated, err := r.data.client.UpdateProjectKey(ctx, r.data.org, projectID, key.ID, sentry.UpdateProjectKeyInput{
			Name: plan.Name.ValueString(), IsActive: false, RateLimit: plan.rateLimit(),
		})
		if err != nil {
			// The key exists: record it so it is not orphaned.
			resp.Diagnostics.Append(plan.fill(ctx, key)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.AddError("Unable to deactivate the new client key", err.Error())
			return
		}
		key = updated
	}
	ignored := rateLimitIgnored(plan.rateLimit(), key)
	resp.Diagnostics.Append(plan.fill(ctx, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if ignored {
		resp.Diagnostics.AddAttributeError(path.Root("rate_limit"), rateLimitIgnoredSummary, rateLimitIgnoredDetail)
	}
}

func (r *projectKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.data.client.GetProjectKey(ctx, r.data.org, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read client key", err.Error())
		return
	}
	resp.Diagnostics.Append(state.fill(ctx, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.data.client.UpdateProjectKey(ctx, r.data.org, plan.ProjectID.ValueString(), plan.ID.ValueString(), sentry.UpdateProjectKeyInput{
		Name: plan.Name.ValueString(), IsActive: plan.IsActive.ValueBool(), RateLimit: plan.rateLimit(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to update client key", err.Error())
		return
	}
	ignored := rateLimitIgnored(plan.rateLimit(), key)
	resp.Diagnostics.Append(plan.fill(ctx, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if ignored {
		resp.Diagnostics.AddAttributeError(path.Root("rate_limit"), rateLimitIgnoredSummary, rateLimitIgnoredDetail)
	}
}

func (r *projectKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.data.client.DeleteProjectKey(ctx, r.data.org, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete client key", err.Error())
	}
}

func (r *projectKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, keyID, ok := strings.Cut(req.ID, "/")
	if !ok || project == "" || keyID == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<project_id_or_slug>/<key_id>`, got %q.", req.ID))
		return
	}
	key, err := r.data.client.GetProjectKey(ctx, r.data.org, project, keyID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import client key", err.Error())
		return
	}
	var state projectKeyModel
	resp.Diagnostics.Append(state.fill(ctx, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
