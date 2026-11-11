// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestTeamLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	config := func(body string) string {
		return f.providerConfig() + "resource \"sentry_team\" \"backend\" {\n" + body + "\n}\n"
	}
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.snapshotTeam(id) != nil {
				return fmt.Errorf("team %s still exists", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Sentry derives the slug from the name.
				Config: config(`name = "Backend Team"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_team.backend", "slug", "backend-team"),
					resource.TestCheckResourceAttrWith("sentry_team.backend", "id", func(v string) error {
						id = v
						if f.snapshotTeam(v) == nil {
							return fmt.Errorf("no team with id %s", v)
						}
						return nil
					}),
				),
			},
			{
				Config: config(`name = "Backend Team"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Import by slug settles on the numeric ID.
				ResourceName:      "sentry_team.backend",
				ImportState:       true,
				ImportStateId:     "backend-team",
				ImportStateVerify: true,
			},
			{
				// Renaming keeps the derived slug and the ID.
				Config: config(`name = "Platform"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("sentry_team.backend", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_team.backend", "slug", "backend-team"),
					resource.TestCheckResourceAttrWith("sentry_team.backend", "id", func(v string) error {
						if v != id {
							return fmt.Errorf("id changed from %s to %s", id, v)
						}
						if got := f.snapshotTeam(id).Name; got != "Platform" {
							return fmt.Errorf("name = %q", got)
						}
						return nil
					}),
				),
			},
			{
				// A slug change is an in-place update too: the ID is the identity.
				Config: config("name = \"Platform\"\n  slug = \"platform\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("sentry_team.backend", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if got := f.snapshotTeam(id).Slug; got != "platform" {
						return fmt.Errorf("slug = %q", got)
					}
					return nil
				},
			},
			{
				// A slug changed in the UI is drift, and is put back.
				PreConfig: func() { f.mutate(func() { f.team(id).Slug = "renamed-in-ui" }) },
				Config:    config("name = \"Platform\"\n  slug = \"platform\""),
				Check: func(*terraform.State) error {
					if got := f.snapshotTeam(id).Slug; got != "platform" {
						return fmt.Errorf("slug = %q", got)
					}
					return nil
				},
			},
			{
				// Deleted in the UI: recreated.
				PreConfig: func() { f.mutate(func() { f.teams = nil }) },
				Config:    config("name = \"Platform\"\n  slug = \"platform\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("sentry_team.backend", plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttrWith("sentry_team.backend", "id", func(v string) error {
					id = v
					return nil
				}),
			},
		},
	})
}

func TestTeamErrors(t *testing.T) {
	f := newFakeSentry(t)
	f.addTeam("Taken")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      f.providerConfig() + "resource \"sentry_team\" \"x\" {\n  name = \"X\"\n  slug = \"Not A Slug\"\n}\n",
				ExpectError: regexp.MustCompile(`lowercase letters`),
			},
			{
				Config:      f.providerConfig() + `resource "sentry_team" "x" { name = "Taken" }`,
				ExpectError: regexp.MustCompile(`(?s)Unable to create team.*already exists`),
			},
		},
	})
}

// TestAccTeamAndProject runs the core lifecycle against the real Sentry API.
func TestAccTeamAndProject(t *testing.T) {
	suffix := randomSuffix()
	config := fmt.Sprintf(`
provider "sentry" {
  organization = %q
}

resource "sentry_team" "acc" {
  name = "tf-acc-%[2]s"
}

resource "sentry_project" "acc" {
  name                = "tf-acc-%[2]s"
  team_id             = sentry_team.acc.id
  default_rules       = false
  deletion_protection = false
}

resource "sentry_project_key" "acc" {
  project_id = sentry_project.acc.id
  name       = "tf-acc"

  rate_limit = {
    window = 60
    count  = 100
  }
}
`, os.Getenv("SENTRY_TEST_ORG"), suffix)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: requiredProviders + config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_team.acc", "slug", "tf-acc-"+suffix),
					resource.TestCheckResourceAttrPair("sentry_project.acc", "team_id", "sentry_team.acc", "id"),
					resource.TestCheckResourceAttrSet("sentry_project_key.acc", "dsn_public"),
				),
			},
			{
				Config: requiredProviders + config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
