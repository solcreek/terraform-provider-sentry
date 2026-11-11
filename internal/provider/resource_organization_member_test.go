// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOrganizationMemberLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	f.addMember("owner@example.com", "owner")

	config := func(body string) string {
		return f.providerConfig() + "resource \"sentry_organization_member\" \"jane\" {\n" + body + "\n}\n" + `
data "sentry_organization_member" "owner" {
  email = "Owner@example.com"
}
`
	}
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.snapshotMember(id) != nil {
				return fmt.Errorf("member %s still exists", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Sentry lowercases the address; the configured spelling is kept.
				Config: config(`  email = "Jane@Example.com"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_organization_member.jane", "id", func(v string) error {
						id = v
						m := f.snapshotMember(v)
						if m == nil || m.Email != "jane@example.com" || m.Role != "member" {
							return fmt.Errorf("member = %+v", m)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("sentry_organization_member.jane", "email", "Jane@Example.com"),
					resource.TestCheckResourceAttr("sentry_organization_member.jane", "role", "member"),
					resource.TestCheckResourceAttr("sentry_organization_member.jane", "pending", "true"),
					resource.TestCheckResourceAttr("data.sentry_organization_member.owner", "role", "owner"),
					resource.TestCheckResourceAttr("data.sentry_organization_member.owner", "pending", "false"),
				),
			},
			{
				Config:           config(`  email = "Jane@Example.com"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Import by email; the imported state has Sentry's spelling.
				ResourceName:            "sentry_organization_member.jane",
				ImportState:             true,
				ImportStateId:           "JANE@example.com",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"email"},
			},
			{
				// The invitation was accepted: no change to make.
				PreConfig:        func() { f.mutate(func() { f.member(id).Pending = false }) },
				Config:           config(`  email = "Jane@Example.com"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr("sentry_organization_member.jane", "pending", "false"),
			},
			{
				Config: config("  email = \"Jane@Example.com\"\n  role  = \"admin\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_organization_member.jane", plancheck.ResourceActionUpdate),
				}},
				Check: func(*terraform.State) error {
					if got := f.snapshotMember(id).Role; got != "admin" {
						return fmt.Errorf("role = %q", got)
					}
					return nil
				},
			},
			{
				// Another address is another member.
				Config: config("  email = \"joe@example.com\"\n  role  = \"admin\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_organization_member.jane", plancheck.ResourceActionReplace),
				}},
				Check: resource.TestCheckResourceAttrWith("sentry_organization_member.jane", "id", func(v string) error {
					if f.snapshotMember(id) != nil {
						return fmt.Errorf("the replaced member was not removed")
					}
					id = v
					return nil
				}),
			},
		},
	})
}

func TestOrganizationMemberErrors(t *testing.T) {
	f := newFakeSentry(t)
	f.addMember("owner@example.com", "owner")
	one := func(body string) string {
		return f.providerConfig() + "resource \"sentry_organization_member\" \"x\" {\n" + body + "\n}\n"
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: one(`  email = "not an email"`), ExpectError: regexp.MustCompile(`must be an email address`)},
			{Config: one("  email = \"a@example.com\"\n  role  = \"root\""), ExpectError: regexp.MustCompile(`(?i)value must be one of`)},
			{Config: one(`  email = "owner@example.com"`), ExpectError: regexp.MustCompile(`(?s)Unable to invite organization member.*already\s+been\s+invited`)},
			{
				Config:      f.providerConfig() + `data "sentry_organization_member" "x" { email = "nobody@example.com" }`,
				ExpectError: regexp.MustCompile(`Organization member not found`),
			},
			{Config: one(`  email = "a@example.com"`)},
		},
	})
}
