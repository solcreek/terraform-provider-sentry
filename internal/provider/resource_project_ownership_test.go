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

func TestProjectOwnershipLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))

	config := func(body string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "sentry_project_ownership" "api" {
  project_id = %q
%s
}
`, p.ID, body)
	}
	rules := `  raw = "path:src/checkout/* #backend\nurl:https://example.com/pay #backend"`
	ownership := func() fakeOwnership { return f.snapshotProject(p.ID).Ownership }

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			o := ownership()
			if o.Raw != nil {
				return fmt.Errorf("rules were not cleared: %q", *o.Raw)
			}
			// Destroying only clears the rules.
			if o.Fallthrough || o.AutoAssignment != "Turn off Auto-Assignment" {
				return fmt.Errorf("other settings were reset: %+v", o)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Only the rules are set: Sentry's other settings are adopted.
				Config: config(rules),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_ownership.api", "id", p.ID),
					resource.TestCheckResourceAttr("sentry_project_ownership.api", "fallthrough", "true"),
					resource.TestCheckResourceAttr("sentry_project_ownership.api", "auto_assignment", "Auto Assign to Issue Owner"),
					resource.TestCheckResourceAttr("sentry_project_ownership.api", "codeowners_auto_sync", "true"),
					func(*terraform.State) error {
						if got := *ownership().Raw; got != "path:src/checkout/* #backend\nurl:https://example.com/pay #backend" {
							return fmt.Errorf("raw = %q", got)
						}
						return nil
					},
				),
			},
			{
				Config:           config(rules),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				ResourceName:      "sentry_project_ownership.api",
				ImportState:       true,
				ImportStateId:     "api",
				ImportStateVerify: true,
			},
			{
				Config: config(rules + "\n  fallthrough     = false\n  auto_assignment = \"Turn off Auto-Assignment\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_project_ownership.api", plancheck.ResourceActionUpdate),
				}},
				Check: func(*terraform.State) error {
					if o := ownership(); o.Fallthrough || o.AutoAssignment != "Turn off Auto-Assignment" || !o.CodeownersAutoSync {
						return fmt.Errorf("ownership = %+v", o)
					}
					return nil
				},
			},
			{
				// Rules edited in the UI are drift.
				PreConfig: func() {
					f.mutate(func() { edited := "path:* #someone-else"; f.project(p.ID).Ownership.Raw = &edited })
				},
				Config: config(rules + "\n  fallthrough     = false\n  auto_assignment = \"Turn off Auto-Assignment\""),
				Check: func(*terraform.State) error {
					if got := *ownership().Raw; got == "path:* #someone-else" {
						return fmt.Errorf("drift was not corrected")
					}
					return nil
				},
			},
			{
				Config:      config(`  raw = "not-a-rule"`),
				ExpectError: regexp.MustCompile(`(?s)Unable to update project ownership.*Parse error`),
			},
			{
				Config:      config(rules + "\n  auto_assignment = \"sometimes\""),
				ExpectError: regexp.MustCompile(`(?i)value must be one of`),
			},
			{
				Config: config(rules + "\n  fallthrough     = false\n  auto_assignment = \"Turn off Auto-Assignment\""),
			},
		},
	})
}
