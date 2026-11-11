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

func TestTeamMemberLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	backend, sre := f.addTeam("Backend"), f.addTeam("SRE")
	// Already on SRE, which this configuration does not manage.
	jane := f.addMember("jane@example.com", "member", sre)

	config := func(teamID string) string {
		return f.providerConfig() + fmt.Sprintf(`
data "sentry_organization_member" "jane" {
  email = "jane@example.com"
}

resource "sentry_team_member" "jane" {
  team_id   = %q
  member_id = data.sentry_organization_member.jane.id
}
`, teamID)
	}
	teamsAre := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got := f.snapshotMember(jane.ID).TeamIDs
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				return fmt.Errorf("member teams = %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             teamsAre(sre.ID),
		Steps: []resource.TestStep{
			{
				Config: config(backend.ID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_team_member.jane", "id", backend.ID+"/"+jane.ID),
					teamsAre(backend.ID, sre.ID),
				),
			},
			{
				Config:           config(backend.ID),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Import by team slug settles on the team ID.
				ResourceName:      "sentry_team_member.jane",
				ImportState:       true,
				ImportStateId:     "backend/" + jane.ID,
				ImportStateVerify: true,
			},
			{
				// The team was renamed: membership is tracked by ID, nothing changes.
				PreConfig:        func() { f.mutate(func() { f.team(backend.ID).Slug = "backend-renamed" }) },
				Config:           config(backend.ID),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Removed in the UI: put back.
				PreConfig: func() { f.mutate(func() { f.member(jane.ID).TeamIDs = []string{sre.ID} }) },
				Config:    config(backend.ID),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_team_member.jane", plancheck.ResourceActionCreate),
				}},
				Check: teamsAre(backend.ID, sre.ID),
			},
		},
	})
}

func TestTeamMemberErrors(t *testing.T) {
	f := newFakeSentry(t)
	backend, sre := f.addTeam("Backend"), f.addTeam("SRE")
	jane := f.addMember("jane@example.com", "member", sre)
	config := func(teamID string) string {
		return f.providerConfig() + fmt.Sprintf("resource \"sentry_team_member\" \"x\" {\n  team_id   = %q\n  member_id = %q\n}\n", teamID, jane.ID)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Existing memberships are not adopted silently.
				Config:      config(sre.ID),
				ExpectError: regexp.MustCompile(`Member is already on the team`),
			},
			{
				// A 202 is an access request, not a membership.
				PreConfig:   func() { f.mutate(func() { f.closedTeams = true }) },
				Config:      config(backend.ID),
				ExpectError: regexp.MustCompile(`Sentry did not add the member to the team`),
			},
			{
				PreConfig: func() { f.mutate(func() { f.closedTeams = false }) },
				Config:    config(backend.ID),
			},
		},
	})
}
