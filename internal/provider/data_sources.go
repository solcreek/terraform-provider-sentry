// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

// baseDataSource holds the provider data every data source needs.
type baseDataSource struct {
	data *providerData
}

func (d *baseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

// idOrSlugValidators requires exactly one of `id` and `slug`.
func idOrSlugValidators() []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("slug")),
	}
}

// -------------------------------------------------------- sentry_organization

type organizationDataSource struct{ baseDataSource }

func newOrganizationDataSource() datasource.DataSource { return &organizationDataSource{} }

type organizationDataModel struct {
	ID   types.String `tfsdk:"id"`
	Slug types.String `tfsdk:"slug"`
	Name types.String `tfsdk:"name"`
}

func (d *organizationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *organizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up the organization the provider is configured for.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Organization ID."},
			"slug": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization slug."},
			"name": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization name."},
		},
	}
}

func (d *organizationDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	org, err := d.data.client.GetOrganization(ctx, d.data.org)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &organizationDataModel{
		ID:   types.StringValue(org.ID),
		Slug: types.StringValue(org.Slug),
		Name: types.StringValue(org.Name),
	})...)
}

// ---------------------------------------------------------------- sentry_team

type teamDataSource struct{ baseDataSource }

func newTeamDataSource() datasource.DataSource { return &teamDataSource{} }

func (d *teamDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (d *teamDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a team by slug or ID.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Team ID. Set this or `slug`."},
			"slug": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Team slug. Set this or `id`."},
			"name": schema.StringAttribute{Computed: true, MarkdownDescription: "Display name of the team."},
		},
	}
}

func (d *teamDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOrSlugValidators()
}

func (d *teamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg teamModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ref := firstNonEmpty(cfg.ID.ValueString(), cfg.Slug.ValueString())
	team, err := d.data.client.GetTeam(ctx, d.data.org, ref)
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.Diagnostics.AddError("Team not found", fmt.Sprintf("Organization %s has no team %q.", d.data.org, ref))
			return
		}
		resp.Diagnostics.AddError("Unable to read team", err.Error())
		return
	}
	cfg.fill(team)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ------------------------------------------------------------- sentry_project

type projectDataSource struct{ baseDataSource }

func newProjectDataSource() datasource.DataSource { return &projectDataSource{} }

type projectDataModel struct {
	ID       types.String `tfsdk:"id"`
	Slug     types.String `tfsdk:"slug"`
	Name     types.String `tfsdk:"name"`
	Platform types.String `tfsdk:"platform"`
	TeamIDs  types.List   `tfsdk:"team_ids"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a project by slug or ID.",
		Attributes: map[string]schema.Attribute{
			"id":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Project ID. Set this or `slug`."},
			"slug":     schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Project slug. Set this or `id`."},
			"name":     schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the project."},
			"platform": schema.StringAttribute{Computed: true, MarkdownDescription: "Platform of the project, if one is set."},
			"team_ids": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "IDs of the teams with access to the project, sorted.",
			},
		},
	}
}

func (d *projectDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return idOrSlugValidators()
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg projectDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ref := firstNonEmpty(cfg.ID.ValueString(), cfg.Slug.ValueString())
	project, err := d.data.client.GetProject(ctx, d.data.org, ref)
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.Diagnostics.AddError("Project not found", fmt.Sprintf("Organization %s has no project %q.", d.data.org, ref))
			return
		}
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	teams, diags := types.ListValueFrom(ctx, types.StringType, teamIDs(project))
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &projectDataModel{
		ID:       types.StringValue(project.ID),
		Slug:     types.StringValue(project.Slug),
		Name:     types.StringValue(project.Name),
		Platform: stringOrNull(project.Platform),
		TeamIDs:  teams,
	})...)
}

// --------------------------------------------------------- sentry_project_key

type projectKeyDataSource struct{ baseDataSource }

func newProjectKeyDataSource() datasource.DataSource { return &projectKeyDataSource{} }

type projectKeyDataModel struct {
	Project   types.String `tfsdk:"project"`
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	ProjectID types.String `tfsdk:"project_id"`
	IsActive  types.Bool   `tfsdk:"is_active"`
	PublicKey types.String `tfsdk:"public_key"`
	SecretKey types.String `tfsdk:"secret_key"`
	DSNPublic types.String `tfsdk:"dsn_public"`
	DSNSecret types.String `tfsdk:"dsn_secret"`
	Endpoints types.Map    `tfsdk:"endpoints"`
}

func (d *projectKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_key"
}

func (d *projectKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a client key of a project, for example the `Default` key Sentry creates " +
			"with every project, to read its DSN. With neither `id` nor `name`, the project must have exactly one key.",
		Attributes: map[string]schema.Attribute{
			"project": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID or slug of the project.",
			},
			"id": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Client key ID (its public key). Conflicts with `name`.",
			},
			"name": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Name of the client key (case-insensitive, exact). Conflicts with `id`.",
			},
			"project_id": schema.StringAttribute{Computed: true, MarkdownDescription: "ID of the project the key belongs to."},
			"is_active":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the key accepts events."},
			"public_key": schema.StringAttribute{Computed: true, MarkdownDescription: "Public key."},
			"secret_key": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Secret key."},
			"dsn_public": schema.StringAttribute{Computed: true, MarkdownDescription: "The DSN to configure SDKs with."},
			"dsn_secret": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Legacy DSN that embeds the secret key."},
			"endpoints": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The key's other ingestion endpoints by name, as Sentry reports them.",
			},
		},
	}
}

func (d *projectKeyDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.Conflicting(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

func (d *projectKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg projectKeyDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	keys, err := d.data.client.ListProjectKeys(ctx, d.data.org, project)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list client keys", err.Error())
		return
	}

	var matches []sentry.ProjectKey
	var wanted string
	switch {
	case cfg.ID.ValueString() != "":
		wanted = fmt.Sprintf("with ID %q", cfg.ID.ValueString())
		for _, k := range keys {
			if k.ID == cfg.ID.ValueString() {
				matches = append(matches, k)
			}
		}
	case cfg.Name.ValueString() != "":
		wanted = fmt.Sprintf("named %q", cfg.Name.ValueString())
		for _, k := range keys {
			if strings.EqualFold(k.Name, cfg.Name.ValueString()) {
				matches = append(matches, k)
			}
		}
	default:
		matches = keys
	}
	switch {
	case len(matches) == 0:
		resp.Diagnostics.AddError("Client key not found",
			strings.TrimSpace(fmt.Sprintf("Project %s has no client key %s", project, wanted))+".")
		return
	case len(matches) > 1:
		resp.Diagnostics.AddError("More than one client key matches",
			fmt.Sprintf("Project %s has %d matching client keys; select one with `id` or `name`.", project, len(matches)))
		return
	}

	k := &matches[0]
	endpoints, diags := keyEndpoints(ctx, k)
	resp.Diagnostics.Append(diags...)
	cfg.ID = types.StringValue(k.ID)
	cfg.Name = types.StringValue(k.Name)
	cfg.ProjectID = types.StringValue(k.ProjectIDString())
	cfg.IsActive = types.BoolValue(k.IsActive)
	cfg.PublicKey = types.StringValue(k.Public)
	cfg.SecretKey = types.StringValue(k.Secret)
	cfg.DSNPublic = types.StringValue(k.DSN["public"])
	cfg.DSNSecret = types.StringValue(k.DSN["secret"])
	cfg.Endpoints = endpoints
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ------------------------------------------------- sentry_organization_member

type organizationMemberDataSource struct{ baseDataSource }

func newOrganizationMemberDataSource() datasource.DataSource { return &organizationMemberDataSource{} }

func (d *organizationMemberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (d *organizationMemberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a member of the organization by email address, for example to put an " +
			"existing member on a team with `sentry_team_member`.",
		Attributes: map[string]schema.Attribute{
			"email":   schema.StringAttribute{Required: true, MarkdownDescription: "Email address of the member (case-insensitive)."},
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Organization member ID. It is not the user's ID."},
			"role":    schema.StringAttribute{Computed: true, MarkdownDescription: "Organization role of the member."},
			"pending": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the member's invitation is still waiting to be accepted."},
		},
	}
}

func (d *organizationMemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg organizationMemberModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	member, err := d.data.client.FindMemberByEmail(ctx, d.data.org, cfg.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to list organization members", err.Error())
		return
	}
	if member == nil {
		resp.Diagnostics.AddError("Organization member not found",
			fmt.Sprintf("Organization %s has no member with the email address %s.", d.data.org, cfg.Email.ValueString()))
		return
	}
	cfg.fill(member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
