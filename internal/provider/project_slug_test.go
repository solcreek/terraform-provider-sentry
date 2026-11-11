// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// TestProjectReferencedBySlug covers every resource that takes a
// `project_id`: a slug works in its place, is kept as written, and plans
// clean afterwards.
func TestProjectReferencedBySlug(t *testing.T) {
	bodies := map[string]string{
		"sentry_project_data_scrubbing": `safe_fields = ["request_id"]`,
		"sentry_project_ownership":      `raw = "path:src/* #backend"`,
		"sentry_project_inbound_filter": "filter_id = \"localhost\"\n  active = true",
		"sentry_project_key":            `name = "Browser"`,
		"sentry_project_team":           "", // team_id is added below
	}
	for typeName, body := range bodies {
		t.Run(typeName, func(t *testing.T) {
			f := newFakeSentry(t)
			p := f.addProject("API", f.addTeam("Backend"))
			if typeName == "sentry_project_team" {
				body = fmt.Sprintf("team_id = %q", f.addTeam("Frontend").ID)
			}
			config := f.providerConfig() + fmt.Sprintf("resource %q \"x\" {\n  project_id = %q\n  %s\n}\n", typeName, p.Slug, body)
			name := typeName + ".x"

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: config, Check: resource.TestCheckResourceAttr(name, "project_id", p.Slug)},
					{
						Config:           config,
						ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
					},
				},
			})
		})
	}
}
