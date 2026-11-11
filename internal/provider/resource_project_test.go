// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestProjectLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	backend, mobile, sre := f.addTeam("Backend"), f.addTeam("Mobile"), f.addTeam("SRE")

	config := func(name, owner string, protect bool, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "sentry_project" "api" {
  name                = %q
  platform            = "python-django"
  team_id             = %q
  default_rules       = false
  deletion_protection = %t
}
%s`, name, owner, protect, extra)
	}
	sreAccess := fmt.Sprintf(`
resource "sentry_project_team" "sre" {
  project_id = sentry_project.api.id
  team_id    = %q
}
`, sre.ID)

	var id string
	teamsAre := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got := f.snapshotProject(id).TeamIDs
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				return fmt.Errorf("project teams = %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.snapshotProject(id) != nil {
				return fmt.Errorf("project %s still exists", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("API", backend.ID, true, sreAccess),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_project.api", "id", func(v string) error {
						id = v
						p := f.snapshotProject(v)
						if p == nil {
							return fmt.Errorf("no project with id %s", v)
						}
						if p.DefaultRules {
							return fmt.Errorf("default_rules = false was not sent")
						}
						return nil
					}),
					resource.TestCheckResourceAttr("sentry_project.api", "slug", "api"),
					resource.TestCheckResourceAttr("sentry_project.api", "platform", "python-django"),
					resource.TestCheckResourceAttr("sentry_project.api", "deletion_protection", "true"),
					func(s *terraform.State) error {
						return resource.TestCheckResourceAttr("sentry_project_team.sre", "id", id+"/"+sre.ID)(s)
					},
					teamsAre(backend.ID, sre.ID),
				),
			},
			{
				Config: config("API", backend.ID, true, sreAccess),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Import by slug. The owner is the first team Sentry lists;
				// default_rules is creation-only and cannot be read back.
				ResourceName:            "sentry_project.api",
				ImportState:             true,
				ImportStateId:           "api",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"default_rules"},
			},
			{
				ResourceName:      "sentry_project_team.sre",
				ImportState:       true,
				ImportStateId:     "api/sre",
				ImportStateVerify: true,
			},
			{
				// Rename and move to another team in place; the extra team stays.
				Config: config("Public API", mobile.ID, true, sreAccess),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("sentry_project.api", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("sentry_project_team.sre", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project.api", "slug", "api"),
					func(*terraform.State) error {
						if got := f.snapshotProject(id).Name; got != "Public API" {
							return fmt.Errorf("name = %q", got)
						}
						return nil
					},
					teamsAre(mobile.ID, sre.ID),
				),
			},
			{
				// Access revoked in the UI, for both teams: both are restored.
				PreConfig: func() { f.mutate(func() { f.project(id).TeamIDs = nil }) },
				Config:    config("Public API", mobile.ID, true, sreAccess),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("sentry_project.api", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("sentry_project_team.sre", plancheck.ResourceActionCreate),
					},
				},
				Check: teamsAre(mobile.ID, sre.ID),
			},
			{
				// Dropping the extra team leaves the owner alone.
				Config: config("Public API", mobile.ID, true, ""),
				Check:  teamsAre(mobile.ID),
			},
			{
				// Protected projects are not destroyed.
				Config:      config("Public API", mobile.ID, true, ""),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Project is protected from deletion`),
			},
			{
				Config: config("Public API", mobile.ID, false, ""),
			},
		},
	})
}

func TestProjectTeamRefusesTheOwner(t *testing.T) {
	f := newFakeSentry(t)
	backend := f.addTeam("Backend")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + fmt.Sprintf(`
resource "sentry_project" "api" {
  name                = "API"
  team_id             = %[1]q
  deletion_protection = false
}

resource "sentry_project_team" "dup" {
  project_id = sentry_project.api.id
  team_id    = %[1]q
}
`, backend.ID),
			ExpectError: regexp.MustCompile(`Team already has access to the project`),
		}},
	})
}

func TestProjectWithoutPlatform(t *testing.T) {
	f := newFakeSentry(t)
	backend := f.addTeam("Backend")
	config := f.providerConfig() + fmt.Sprintf(`
resource "sentry_project" "api" {
  name                = "API"
  slug                = "edge-api"
  team_id             = %q
  deletion_protection = false
}
`, backend.ID)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project.api", "slug", "edge-api"),
					resource.TestCheckNoResourceAttr("sentry_project.api", "platform"),
					resource.TestCheckNoResourceAttr("sentry_project.api", "default_rules"),
				),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
