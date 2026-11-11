// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
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

// TestAccProjectSettings takes over the ownership rules and inbound filters
// of a throwaway project.
func TestAccProjectSettings(t *testing.T) {
	testAccPreCheck(t)
	name := "tf-acc-" + randomSuffix()

	config := func(rules, fallThrough, crawlers, browsers string) string {
		return requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %[1]q
}

resource "sentry_team" "acc" {
  name = %[2]q
}

resource "sentry_project" "acc" {
  name                = %[2]q
  team_id             = sentry_team.acc.id
  default_rules       = false
  deletion_protection = false
}

resource "sentry_project_ownership" "acc" {
  project_id  = sentry_project.acc.id
  raw         = %[3]s
  fallthrough = %[4]s
}

resource "sentry_project_inbound_filter" "crawlers" {
  project_id = sentry_project.acc.id
  filter_id  = "web-crawlers"
  active     = %[5]s
}

resource "sentry_project_inbound_filter" "legacy" {
  project_id = sentry_project.acc.id
  filter_id  = "legacy-browsers"
  subfilters = [%[6]s]
}
`, os.Getenv("SENTRY_TEST_ORG"), name, rules, fallThrough, crawlers, browsers)
	}
	// Two rules on two lines, written the way a heredoc would produce them.
	twoRules := fmt.Sprintf("\"path:src/checkout/* #%[1]s\\nurl:https://example.com/pay #%[1]s\\n\"", name)
	oneRule := fmt.Sprintf("\"path:src/* #%s\"", name)
	first := config(twoRules, "false", "true", `"ie", "opera_mini"`)
	second := config(oneRule, "true", "false", `"safari"`)
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: first,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_ownership.acc", "fallthrough", "false"),
					resource.TestCheckResourceAttrSet("sentry_project_ownership.acc", "auto_assignment"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.crawlers", "active", "true"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.legacy", "subfilters.#", "2"),
				),
			},
			{Config: first, ConfigPlanChecks: empty},
			{
				ResourceName: "sentry_project_ownership.acc", ImportState: true, ImportStateId: name, ImportStateVerify: true,
				// The imported text is Sentry's, without the trailing newline.
				ImportStateVerifyIgnore: []string{"raw"},
			},
			{ResourceName: "sentry_project_inbound_filter.crawlers", ImportState: true, ImportStateId: name + "/web-crawlers", ImportStateVerify: true},
			{ResourceName: "sentry_project_inbound_filter.legacy", ImportState: true, ImportStateId: name + "/legacy-browsers", ImportStateVerify: true},
			{
				Config: second,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_ownership.acc", "fallthrough", "true"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.crawlers", "active", "false"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.legacy", "subfilters.#", "1"),
				),
			},
			{Config: second, ConfigPlanChecks: empty},
			{
				// An empty browser set turns legacy-browsers off and settles.
				Config: config(oneRule, "true", "false", ``),
				Check:  resource.TestCheckResourceAttr("sentry_project_inbound_filter.legacy", "subfilters.#", "0"),
			},
			{Config: config(oneRule, "true", "false", ``), ConfigPlanChecks: empty},
		},
	})
}

// TestAccTeamMember puts an existing organization member, named by
// SENTRY_TEST_MEMBER_ID, on a throwaway team and takes them off again. It
// never invites or removes an organization member.
//
// A team created with a personal token starts with the token's user on it
// (observed on sentry.io, 2026-11-11), so when the test member is that user
// they are taken off the new team first.
func TestAccTeamMember(t *testing.T) {
	testAccPreCheck(t)
	memberID := os.Getenv("SENTRY_TEST_MEMBER_ID")
	if memberID == "" {
		t.Skip("SENTRY_TEST_MEMBER_ID (an existing organization member's ID) is not set")
	}
	client, org := testAccClient()
	ctx := context.Background()
	name := "tf-acc-" + randomSuffix()

	before, err := client.GetMember(ctx, org, memberID)
	if err != nil {
		t.Fatalf("read member %s: %v", memberID, err)
	}
	onTeam := func() (bool, error) {
		m, err := client.GetMember(ctx, org, memberID)
		if err != nil {
			return false, err
		}
		return m.InTeam(name), nil
	}

	header := requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %q
}

resource "sentry_team" "acc" {
  name = %q
}
`, org, name)
	withMember := header + fmt.Sprintf(`
resource "sentry_team_member" "acc" {
  team_id   = sentry_team.acc.id
  member_id = %q
}
`, memberID)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			after, err := client.GetMember(ctx, org, memberID)
			if err != nil {
				return fmt.Errorf("the member must still exist after destroy: %w", err)
			}
			if after.OrgRole != before.OrgRole || len(after.Teams) != len(before.Teams) {
				return fmt.Errorf("member changed: role %s -> %s, teams %d -> %d", before.OrgRole, after.OrgRole, len(before.Teams), len(after.Teams))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: header,
				Check: func(*terraform.State) error {
					on, err := onTeam()
					t.Logf("member on the team right after it was created: %t", on)
					return err
				},
			},
			{
				PreConfig: func() {
					if on, _ := onTeam(); on {
						if err := client.RemoveTeamMember(ctx, org, memberID, name); err != nil {
							t.Fatalf("take the member off the new team: %v", err)
						}
					}
				},
				Config: withMember,
				Check: func(*terraform.State) error {
					if on, err := onTeam(); err != nil || !on {
						return fmt.Errorf("member is not on team %s (err %v)", name, err)
					}
					return nil
				},
			},
			{Config: withMember, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{ResourceName: "sentry_team_member.acc", ImportState: true, ImportStateId: name + "/" + memberID, ImportStateVerify: true},
			{
				// Dropping the resource takes the member off the team.
				Config: header,
				Check: func(*terraform.State) error {
					if on, err := onTeam(); err != nil || on {
						return fmt.Errorf("member is still on team %s (err %v)", name, err)
					}
					return nil
				},
			},
		},
	})
}

// TestAccOrganizationMember invites SENTRY_TEST_INVITE_EMAIL, which must not
// be a member yet, puts the pending member on a throwaway team, changes the
// role and withdraws the invitation. Sentry emails that address.
func TestAccOrganizationMember(t *testing.T) {
	testAccPreCheck(t)
	email := os.Getenv("SENTRY_TEST_INVITE_EMAIL")
	if email == "" {
		t.Skip("SENTRY_TEST_INVITE_EMAIL (an address that is not a member yet) is not set")
	}
	client, org := testAccClient()
	ctx := context.Background()
	if existing, err := client.FindMemberByEmail(ctx, org, email); err != nil {
		t.Fatalf("list members: %v", err)
	} else if existing != nil {
		t.Fatalf("SENTRY_TEST_INVITE_EMAIL is already member %s of %s; refusing to touch an existing member", existing.ID, org)
	}
	name := "tf-acc-" + randomSuffix()

	config := func(role string) string {
		return requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %q
}

resource "sentry_organization_member" "acc" {
  email = %q
  role  = %q
}

resource "sentry_team" "acc" {
  name = %q
}

resource "sentry_team_member" "acc" {
  team_id   = sentry_team.acc.id
  member_id = sentry_organization_member.acc.id
}

data "sentry_organization_member" "acc" {
  email      = %q
  depends_on = [sentry_organization_member.acc]
}
`, org, email, role, name, email)
	}
	var memberID string
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if m, err := client.FindMemberByEmail(ctx, org, email); err != nil || m != nil {
				return fmt.Errorf("the invitation was not withdrawn (member %v, err %v)", m, err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("member"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_organization_member.acc", "id", func(v string) error { memberID = v; return nil }),
					resource.TestCheckResourceAttr("sentry_organization_member.acc", "role", "member"),
					resource.TestCheckResourceAttr("sentry_organization_member.acc", "pending", "true"),
					resource.TestCheckResourceAttrPair("data.sentry_organization_member.acc", "id", "sentry_organization_member.acc", "id"),
				),
			},
			{Config: config("member"), ConfigPlanChecks: empty},
			{ResourceName: "sentry_organization_member.acc", ImportState: true, ImportStateId: email, ImportStateVerify: true},
			{
				ResourceName: "sentry_team_member.acc", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return name + "/" + memberID, nil },
			},
			{
				Config: config("admin"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("sentry_organization_member.acc", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("sentry_organization_member.acc", "role", "admin"),
			},
			{Config: config("admin"), ConfigPlanChecks: empty},
		},
	})
}

// TestAccProjectKeyRateLimit checks a client key rate limit on whichever kind
// of plan the organization has: stored where projects have the rate-limits
// feature, and refused before anything is created where they do not.
func TestAccProjectKeyRateLimit(t *testing.T) {
	testAccPreCheck(t)
	client, org := testAccClient()
	ctx := context.Background()
	name := "tf-acc-" + randomSuffix()

	header := requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %q
}

resource "sentry_team" "acc" {
  name = %q
}

resource "sentry_project" "acc" {
  name                = %q
  team_id             = sentry_team.acc.id
  default_rules       = false
  deletion_protection = false
}
`, org, name, name)
	limited := header + `
resource "sentry_project_key" "acc" {
  project_id = sentry_project.acc.id
  name       = "tf-acc"

  rate_limit = {
    window = 60
    count  = 100
  }
}
`
	available := false
	steps := []resource.TestStep{
		{
			Config: header,
			Check: func(*terraform.State) error {
				p, err := client.GetProject(ctx, org, name)
				if err != nil {
					return err
				}
				available = p.HasFeature(sentry.FeatureRateLimits)
				t.Logf("project has the %s feature: %t", sentry.FeatureRateLimits, available)
				return nil
			},
		},
		{
			// Which of the two outcomes is right depends on the plan, known
			// only after the first step, so both are checked by hand.
			Config:             limited,
			ExpectNonEmptyPlan: true,
			PlanOnly:           true,
		},
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: append(steps, resource.TestStep{
			PreConfig: func() {
				key, err := client.CreateProjectKey(ctx, org, name, "tf-acc-direct", &sentry.RateLimit{Window: 60, Count: 100})
				if err != nil {
					t.Fatalf("create key: %v", err)
				}
				t.Logf("key created directly with a rate limit: stored = %v", key.RateLimit)
				if (key.RateLimit != nil) != available {
					t.Errorf("the rate-limits feature (%t) does not predict whether Sentry stores a rate limit (%t)", available, key.RateLimit != nil)
				}
				if err := client.DeleteProjectKey(ctx, org, name, key.ID); err != nil {
					t.Errorf("delete key: %v", err)
				}
			},
			Config: header,
		}),
	})

	if available {
		t.Log("rate limits are available here; the refusal path was not exercised")
		return
	}
	// The refusal, end to end, on a second throwaway project.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: header},
			{Config: limited, ExpectError: regexp.MustCompile(`Client key rate limits are not available`)},
			{
				PreConfig: func() {
					keys, err := client.ListProjectKeys(ctx, org, name)
					if err != nil {
						t.Fatalf("list keys: %v", err)
					}
					if len(keys) != 1 {
						t.Errorf("the refused apply left %d keys, want only the Default one", len(keys))
					}
				},
				Config: header,
			},
		},
	})
}

// TestAccProjectDataScrubbing takes over the data scrubbing settings of a
// throwaway project. It never touches the organization-wide settings.
func TestAccProjectDataScrubbing(t *testing.T) {
	testAccPreCheck(t)
	name := "tf-acc-" + randomSuffix()
	config := func(body string) string {
		return requiredProviders + fmt.Sprintf(`
provider "sentry" {
  organization = %[1]q
}

resource "sentry_team" "acc" {
  name = %[2]q
}

resource "sentry_project" "acc" {
  name                = %[2]q
  team_id             = sentry_team.acc.id
  default_rules       = false
  deletion_protection = false
}

resource "sentry_project_data_scrubbing" "acc" {
  project_id = sentry_project.acc.id
%[3]s
}
`, os.Getenv("SENTRY_TEST_ORG"), name, body)
	}
	lists := "  safe_fields      = [\"request_id\", \"Build_Number\"]\n  sensitive_fields = [\"session_token\"]"
	switches := "  data_scrubber          = false\n  data_scrubber_defaults = false\n  scrub_ip_addresses     = true\n  safe_fields            = [\"request_id\"]\n  sensitive_fields       = []"
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(lists),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.acc", "safe_fields.#", "2"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.acc", "sensitive_fields.#", "1"),
					resource.TestCheckResourceAttrSet("sentry_project_data_scrubbing.acc", "data_scrubber"),
				),
			},
			{Config: config(lists), ConfigPlanChecks: empty},
			{ResourceName: "sentry_project_data_scrubbing.acc", ImportState: true, ImportStateId: name, ImportStateVerify: true},
			{
				Config: config(switches),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.acc", "data_scrubber", "false"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.acc", "scrub_ip_addresses", "true"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.acc", "sensitive_fields.#", "0"),
				),
			},
			{Config: config(switches), ConfigPlanChecks: empty},
		},
	})
}
