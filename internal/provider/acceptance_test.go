// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

// Acceptance tests call the real Sentry API (see testAccPreCheck). Everything
// they create is named tf-acc-<random> and deleted again.

func testAccClient() (*sentry.Client, string) {
	return sentry.New(os.Getenv("SENTRY_AUTH_TOKEN"), sentry.WithBaseURL(os.Getenv("SENTRY_BASE_URL"))), os.Getenv("SENTRY_TEST_ORG")
}

// TestAccLifecycle creates, imports, renames and destroys every resource.
func TestAccLifecycle(t *testing.T) {
	testAccPreCheck(t)
	suffix := randomSuffix()
	name := "tf-acc-" + suffix

	config := func(teamName, teamSlug, projectName, projectSlug, keyName string, keyActive bool) string {
		return requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %q
}

resource "sentry_team" "owner" {
  name = %q
  slug = %q
}

resource "sentry_team" "extra" {
  name = %q
}

resource "sentry_project" "acc" {
  name                = %q
  slug                = %q
  platform            = "python"
  team_id             = sentry_team.owner.id
  default_rules       = false
  deletion_protection = false
}

resource "sentry_project_team" "extra" {
  project_id = sentry_project.acc.id
  team_id    = sentry_team.extra.id
}

resource "sentry_project_key" "acc" {
  project_id = sentry_project.acc.id
  name       = %q
  is_active  = %t
}

data "sentry_project_key" "default" {
  project = sentry_project.acc.id
  name    = "Default"
}
`, os.Getenv("SENTRY_TEST_ORG"), teamName, teamSlug, name+"-extra", projectName, projectSlug, keyName, keyActive)
	}
	first := config(name, name, name, name, "tf-acc", true)
	renamed := config(name+" renamed", name+"-renamed", name+" renamed", name+"-renamed", "tf-acc renamed", false)

	var teamID, projectID, keyID string
	remember := func(dst *string) func(string) error {
		return func(v string) error { *dst = v; return nil }
	}
	unchanged := func(what string, want *string) func(string) error {
		return func(v string) error {
			if v != *want {
				return fmt.Errorf("%s ID changed from %s to %s", what, *want, v)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			client, org := testAccClient()
			ctx := context.Background()
			// Sentry schedules these deletions; both must stop answering at once.
			if _, err := client.GetProject(ctx, org, projectID); !sentry.IsNotFound(err) {
				return fmt.Errorf("project %s after destroy: err = %v, want a 404", projectID, err)
			}
			if _, err := client.GetTeam(ctx, org, teamID); !sentry.IsNotFound(err) {
				return fmt.Errorf("team %s after destroy: err = %v, want a 404", teamID, err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_team.owner", "id", remember(&teamID)),
					resource.TestCheckResourceAttrWith("sentry_project.acc", "id", remember(&projectID)),
					resource.TestCheckResourceAttrWith("sentry_project_key.acc", "id", remember(&keyID)),
					resource.TestCheckResourceAttr("sentry_team.extra", "slug", name+"-extra"),
					resource.TestCheckResourceAttr("sentry_project.acc", "platform", "python"),
					resource.TestCheckResourceAttrPair("sentry_project.acc", "team_id", "sentry_team.owner", "id"),
					resource.TestCheckResourceAttrSet("sentry_project_key.acc", "dsn_public"),
					resource.TestCheckResourceAttrSet("sentry_project_key.acc", "endpoints.csp"),
					resource.TestCheckResourceAttrPair("data.sentry_project_key.default", "project_id", "sentry_project.acc", "id"),
				),
			},
			{
				Config:           first,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ResourceName: "sentry_team.owner", ImportState: true, ImportStateId: name, ImportStateVerify: true},
			{
				ResourceName: "sentry_project.acc", ImportState: true, ImportStateId: name, ImportStateVerify: true,
				// default_rules cannot be read back, and the owner is whichever
				// team Sentry lists first.
				ImportStateVerifyIgnore: []string{"default_rules", "team_id", "deletion_protection"},
			},
			{ResourceName: "sentry_project_team.extra", ImportState: true, ImportStateId: name + "/" + name + "-extra", ImportStateVerify: true},
			{
				ResourceName: "sentry_project_key.acc", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return name + "/" + keyID, nil },
			},
			{
				// Renames, slug changes and deactivation are in place.
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_team.owner", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("sentry_project.acc", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("sentry_project_key.acc", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("sentry_project_team.extra", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_team.owner", "id", unchanged("team", &teamID)),
					resource.TestCheckResourceAttrWith("sentry_project.acc", "id", unchanged("project", &projectID)),
					resource.TestCheckResourceAttrWith("sentry_project_key.acc", "id", unchanged("key", &keyID)),
					resource.TestCheckResourceAttr("sentry_team.owner", "slug", name+"-renamed"),
					resource.TestCheckResourceAttr("sentry_project.acc", "slug", name+"-renamed"),
					resource.TestCheckResourceAttr("sentry_project.acc", "platform", "python"),
					resource.TestCheckResourceAttr("sentry_project_key.acc", "is_active", "false"),
				),
			},
			{
				Config:           renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// TestAccAPIFacts pins down, with direct API calls, the live behaviours the
// in-memory fake has to mirror, and logs what Sentry answers. Every team and
// project it manages to create, expected or not, is deleted again.
func TestAccAPIFacts(t *testing.T) {
	testAccPreCheck(t)
	client, org := testAccClient()
	ctx := context.Background()
	name := "tf-acc-facts-" + randomSuffix()

	// Deleting twice is harmless: the second answer is a 404.
	keepTeam := func(team *sentry.Team, err error) (*sentry.Team, error) {
		if err == nil {
			t.Cleanup(func() { _ = client.DeleteTeam(ctx, org, team.ID) })
		}
		return team, err
	}
	keepProject := func(p *sentry.Project, err error) (*sentry.Project, error) {
		if err == nil {
			t.Cleanup(func() { _ = client.DeleteProject(ctx, org, p.ID) })
		}
		return p, err
	}
	outcome := func(what string, slug string, err error) {
		if err != nil {
			t.Logf("%s: status %d: %v", what, sentry.StatusCode(err), err)
		} else {
			t.Logf("%s: accepted, slug %q", what, slug)
		}
	}
	team, err := keepTeam(client.CreateTeam(ctx, org, name, ""))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Logf("team created: slug derived = %q (from name %q)", team.Slug, name)

	dup, err := keepTeam(client.CreateTeam(ctx, org, name, ""))
	outcome("second team with the same name, no slug", slugIfTeam(dup), err)
	dup, err = keepTeam(client.CreateTeam(ctx, org, name, team.Slug))
	outcome("second team with the same explicit slug", slugIfTeam(dup), err)
	other, err := keepTeam(client.CreateTeam(ctx, org, name+"-other", ""))
	if err != nil {
		t.Fatalf("create other team: %v", err)
	}
	_, err = client.UpdateTeam(ctx, org, other.ID, other.Name, team.Slug)
	outcome("renaming a team onto a taken slug", team.Slug, err)

	project, err := keepProject(client.CreateProject(ctx, org, team.ID, sentry.CreateProjectInput{Name: name}))
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Logf("project created: slug %q, platform %v, teams in create response = %d", project.Slug, project.Platform, len(project.Teams))

	dupP, err := keepProject(client.CreateProject(ctx, org, team.ID, sentry.CreateProjectInput{Name: name}))
	outcome("second project with the same name, no slug", slugIfProject(dupP), err)
	dupP, err = keepProject(client.CreateProject(ctx, org, team.ID, sentry.CreateProjectInput{Name: name, Slug: project.Slug}))
	outcome("second project with the same explicit slug", slugIfProject(dupP), err)
	otherP, err := keepProject(client.CreateProject(ctx, org, team.ID, sentry.CreateProjectInput{Name: name + "-other"}))
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	_, err = client.UpdateProject(ctx, org, otherP.ID, sentry.UpdateProjectInput{Slug: &project.Slug})
	outcome("renaming a project onto a taken slug", project.Slug, err)

	err = client.AddProjectTeam(ctx, org, project.ID, team.ID)
	t.Logf("adding a team that already has access: status %d: %v", sentry.StatusCode(err), err)
	err = client.RemoveProjectTeam(ctx, org, project.ID, other.ID)
	t.Logf("removing a team that has no access: status %d: %v", sentry.StatusCode(err), err)

	keys, err := client.ListProjectKeys(ctx, org, project.ID)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	for _, k := range keys {
		t.Logf("key of a new project: name %q, active %t, rate limit %v", k.Name, k.IsActive, k.RateLimit)
	}
	_, err = client.GetProjectKey(ctx, org, project.ID, "00000000000000000000000000000000")
	t.Logf("GET unknown key: status %d: %v", sentry.StatusCode(err), err)

	if err := client.RemoveProjectTeam(ctx, org, project.ID, team.ID); err != nil {
		t.Logf("removing the last team: status %d: %v", sentry.StatusCode(err), err)
	} else if p, err := client.GetProject(ctx, org, project.ID); err == nil {
		t.Logf("removing the last team: accepted, project now has %d teams", len(p.Teams))
	}

	if err := client.DeleteProject(ctx, org, project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	_, err = client.GetProject(ctx, org, project.ID)
	t.Logf("GET project right after delete: status %d", sentry.StatusCode(err))
	err = client.DeleteProject(ctx, org, project.ID)
	t.Logf("second DELETE of the project: status %d", sentry.StatusCode(err))
	again, err := keepProject(client.CreateProject(ctx, org, team.ID, sentry.CreateProjectInput{Name: name, Slug: project.Slug}))
	outcome("recreating the project's slug right after delete", slugIfProject(again), err)

	if err := client.DeleteTeam(ctx, org, team.ID); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	_, err = client.GetTeam(ctx, org, team.ID)
	t.Logf("GET team right after delete: status %d", sentry.StatusCode(err))
	reborn, err := keepTeam(client.CreateTeam(ctx, org, name, team.Slug))
	outcome("recreating the team's slug right after delete", slugIfTeam(reborn), err)
}

func slugIfTeam(t *sentry.Team) string {
	if t == nil {
		return ""
	}
	return t.Slug
}

func slugIfProject(p *sentry.Project) string {
	if p == nil {
		return ""
	}
	return p.Slug
}
