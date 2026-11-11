// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestDataSources(t *testing.T) {
	f := newFakeSentry(t)
	backend, sre := f.addTeam("Backend"), f.addTeam("SRE")
	p := f.addProject("API", sre, backend)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + fmt.Sprintf(`
data "sentry_team" "by_slug" {
  slug = "backend"
}

data "sentry_team" "by_id" {
  id = %q
}

data "sentry_project" "api" {
  slug = "api"
}

data "sentry_project_key" "default" {
  project = data.sentry_project.api.id
}
`, sre.ID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.sentry_team.by_slug", "id", backend.ID),
					resource.TestCheckResourceAttr("data.sentry_team.by_slug", "name", "Backend"),
					resource.TestCheckResourceAttr("data.sentry_team.by_id", "slug", "sre"),
					resource.TestCheckResourceAttr("data.sentry_project.api", "id", p.ID),
					resource.TestCheckResourceAttr("data.sentry_project.api", "name", "API"),
					resource.TestCheckNoResourceAttr("data.sentry_project.api", "platform"),
					// Sorted, whatever order Sentry lists them in.
					resource.TestCheckResourceAttr("data.sentry_project.api", "team_ids.#", "2"),
					resource.TestCheckResourceAttr("data.sentry_project.api", "team_ids.0", backend.ID),
					resource.TestCheckResourceAttr("data.sentry_project.api", "team_ids.1", sre.ID),
					resource.TestCheckResourceAttr("data.sentry_project_key.default", "name", "Default"),
					resource.TestCheckResourceAttr("data.sentry_project_key.default", "project_id", p.ID),
					resource.TestCheckResourceAttrSet("data.sentry_project_key.default", "dsn_public"),
				),
			},
			{
				Config:      f.providerConfig() + `data "sentry_project" "x" {}`,
				ExpectError: regexp.MustCompile(`(?i)exactly one of`),
			},
			{
				Config:      f.providerConfig() + `data "sentry_team" "x" { slug = "nope" }`,
				ExpectError: regexp.MustCompile(`Team not found`),
			},
		},
	})
}

func TestProjectKeyDataSourceSelection(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))
	f.mutate(func() {
		for _, name := range []string{"Web", "Worker", "Mobile"} {
			p.Keys = append(p.Keys, f.newKey(name))
		}
		f.pageSize = 2 // the wanted key is on the second page
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
data "sentry_project_key" "mobile" {
  project = "api"
  name    = "mobile"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.sentry_project_key.mobile", "name", "Mobile"),
					resource.TestCheckResourceAttr("data.sentry_project_key.mobile", "is_active", "true"),
				),
			},
			{
				Config:      f.providerConfig() + `data "sentry_project_key" "x" { project = "api" }`,
				ExpectError: regexp.MustCompile(`has 4 matching client keys`),
			},
			{
				Config:      f.providerConfig() + "data \"sentry_project_key\" \"x\" {\n  project = \"api\"\n  name    = \"nope\"\n}\n",
				ExpectError: regexp.MustCompile(`has no client key named "nope"`),
			},
		},
	})
}
