// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

var (
	_ resource.Resource                = (*organizationMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*organizationMemberResource)(nil)
	_ resource.ResourceWithImportState = (*organizationMemberResource)(nil)
)

// orgRoles are Sentry's organization-level roles.
var orgRoles = []string{"billing", "member", "admin", "manager", "owner"}

type organizationMemberResource struct {
	data *providerData
}

func newOrganizationMemberResource() resource.Resource { return &organizationMemberResource{} }

type organizationMemberModel struct {
	ID      types.String `tfsdk:"id"`
	Email   types.String `tfsdk:"email"`
	Role    types.String `tfsdk:"role"`
	Pending types.Bool   `tfsdk:"pending"`
}

// fill copies what Sentry reports. The configured spelling of the email is
// kept when it only differs in case, which Sentry does not preserve.
func (m *organizationMemberModel) fill(member *sentry.Member) {
	m.ID = types.StringValue(member.ID)
	if !strings.EqualFold(m.Email.ValueString(), member.Email) {
		m.Email = types.StringValue(member.Email)
	}
	m.Role = types.StringValue(member.OrgRole)
	m.Pending = types.BoolValue(member.Pending)
}

func (r *organizationMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (r *organizationMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member of the organization. Creating it sends an invitation by email; the " +
			"member is `pending` until it is accepted. Destroying it removes the member or withdraws the " +
			"invitation. Put members on teams with `sentry_team_member`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Organization member ID. It is not the user's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Email address to invite.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(emailPattern, "must be an email address")},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Organization role: `billing`, `member`, `admin`, `manager` or `owner`. " +
					"Defaults to `member`. Sentry only lets a personal token change the role of an existing " +
					"member, and only up to the token owner's own role.",
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString("member"),
				Validators: []validator.String{stringvalidator.OneOf(orgRoles...)},
			},
			"pending": schema.BoolAttribute{
				MarkdownDescription: "Whether the invitation is still waiting to be accepted.",
				Computed:            true,
			},
		},
	}
}

func (r *organizationMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *organizationMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Never adopt an existing member by inviting them again: destroying this
	// resource would then remove someone it did not add.
	existing, err := r.data.client.FindMemberByEmail(ctx, r.data.org, plan.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to list organization members", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("email"), "Already a member of the organization",
			fmt.Sprintf("%s is already a member of the organization (or has a pending invitation). "+
				"To manage that member, import it: terraform import <address> %s", existing.Email, existing.Email))
		return
	}
	member, err := r.data.client.InviteMember(ctx, r.data.org, plan.Email.ValueString(), plan.Role.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to invite organization member", err.Error())
		return
	}
	plan.fill(member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *organizationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state organizationMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	member, err := r.data.client.GetMember(ctx, r.data.org, state.ID.ValueString())
	if err != nil {
		if sentry.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read organization member", err.Error())
		return
	}
	state.fill(member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *organizationMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan organizationMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	member, err := r.data.client.UpdateMemberRole(ctx, r.data.org, plan.ID.ValueString(), plan.Role.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to change the member's organization role", err.Error())
		return
	}
	plan.fill(member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *organizationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state organizationMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.RemoveMember(ctx, r.data.org, state.ID.ValueString()); err != nil && !sentry.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to remove organization member", err.Error())
	}
}

// ImportState accepts an organization member ID or the member's email.
func (r *organizationMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "@") {
		member, err := r.data.client.FindMemberByEmail(ctx, r.data.org, id)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list organization members", err.Error())
			return
		}
		if member == nil {
			resp.Diagnostics.AddError("Organization member not found", "No member of the organization has the email address "+id+".")
			return
		}
		id = member.ID
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
