// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

// Ensure the provider satisfies the framework interface.
var _ provider.Provider = (*sentryProvider)(nil)

type sentryProvider struct {
	version string
}

type providerModel struct {
	AuthToken      types.String `tfsdk:"auth_token"`
	Organization   types.String `tfsdk:"organization"`
	BaseURL        types.String `tfsdk:"base_url"`
	TimeoutSeconds types.Int64  `tfsdk:"timeout_seconds"`
}

// providerData is handed to every resource and data source.
type providerData struct {
	client *sentry.Client
	// org is the ID or slug of the organization everything is managed in.
	org string
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &sentryProvider{version: version}
	}
}

func (p *sentryProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "sentry"
	resp.Version = p.version
}

func (p *sentryProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Sentry teams, projects, team access to projects and client keys (DSNs) " +
			"in one organization through the Sentry API. Authenticates with a Sentry auth token.",
		Attributes: map[string]schema.Attribute{
			"auth_token": schema.StringAttribute{
				MarkdownDescription: "Sentry auth token: the token of an internal integration (recommended) or a " +
					"personal token. Falls back to the `SENTRY_AUTH_TOKEN` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"organization": schema.StringAttribute{
				MarkdownDescription: "Slug or ID of the organization to manage. Falls back to `SENTRY_ORG`.",
				Optional:            true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Sentry base URL. Defaults to `https://sentry.io`; set it for a self-hosted " +
					"install or a region host such as `https://de.sentry.io`. Falls back to `SENTRY_BASE_URL`.",
				Optional: true,
			},
			"timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "Per-request HTTP timeout in seconds. Defaults to 60.",
				Optional:            true,
			},
		},
	}
}

func (p *sentryProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for attr, v := range map[string]types.String{"auth_token": cfg.AuthToken, "organization": cfg.Organization, "base_url": cfg.BaseURL} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Unknown provider configuration",
				fmt.Sprintf("`%s` must be known when the provider is configured; it cannot depend on a value that is only known after apply.", attr))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	token := firstNonEmpty(cfg.AuthToken.ValueString(), os.Getenv("SENTRY_AUTH_TOKEN"))
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("auth_token"),
			"Missing Sentry credentials",
			"Set the provider `auth_token` argument or the SENTRY_AUTH_TOKEN environment variable.",
		)
	}
	org := firstNonEmpty(cfg.Organization.ValueString(), os.Getenv("SENTRY_ORG"))
	if org == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("organization"),
			"Missing Sentry organization",
			"Set the provider `organization` argument or the SENTRY_ORG environment variable to the organization's slug.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	timeout := 60 * time.Second
	if !cfg.TimeoutSeconds.IsNull() && cfg.TimeoutSeconds.ValueInt64() > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds.ValueInt64()) * time.Second
	}

	client := sentry.New(token,
		sentry.WithBaseURL(firstNonEmpty(cfg.BaseURL.ValueString(), os.Getenv("SENTRY_BASE_URL"))),
		sentry.WithTimeout(timeout),
		sentry.WithUserAgent("terraform-provider-sentry/"+p.version),
	)

	data := &providerData{client: client, org: org}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *sentryProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newTeamResource,
		newProjectResource,
		newProjectTeamResource,
		newProjectKeyResource,
	}
}

func (p *sentryProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newOrganizationDataSource,
		newTeamDataSource,
		newProjectDataSource,
		newProjectKeyDataSource,
	}
}

// configureData extracts the provider data in a resource or data source
// Configure method. It returns nil before the provider is configured.
func configureData(raw any, diags *diag.Diagnostics) *providerData {
	if raw == nil {
		return nil
	}
	d, ok := raw.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. Please report this issue to the provider developers.", raw))
		return nil
	}
	return d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
