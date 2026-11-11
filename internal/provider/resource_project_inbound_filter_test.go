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

func TestProjectInboundFilterLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))
	// A filter this configuration does not manage must be left alone.
	f.mutate(func() { p.Filters = map[string]bool{"localhost": true} })

	config := func(crawlers bool, browsers string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "sentry_project_inbound_filter" "crawlers" {
  project_id = %[1]q
  filter_id  = "web-crawlers"
  active     = %[2]t
}

resource "sentry_project_inbound_filter" "legacy" {
  project_id = %[1]q
  filter_id  = "legacy-browsers"
  subfilters = [%[3]s]
}
`, p.ID, crawlers, browsers)
	}
	state := func() *fakeProject { return f.snapshotProject(p.ID) }
	browsersAre := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got := state().LegacyBrowsers
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				return fmt.Errorf("legacy browsers = %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			s := state()
			if s.Filters["web-crawlers"] || len(s.LegacyBrowsers) != 0 {
				return fmt.Errorf("filters were not turned off: %v %v", s.Filters, s.LegacyBrowsers)
			}
			if !s.Filters["localhost"] {
				return fmt.Errorf("an unmanaged filter was changed")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(true, `"ie", "safari"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.crawlers", "id", p.ID+"/web-crawlers"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.crawlers", "active", "true"),
					resource.TestCheckNoResourceAttr("sentry_project_inbound_filter.crawlers", "subfilters.#"),
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.legacy", "subfilters.#", "2"),
					resource.TestCheckNoResourceAttr("sentry_project_inbound_filter.legacy", "active"),
					browsersAre("ie", "safari"),
					func(*terraform.State) error {
						if s := state(); !s.Filters["web-crawlers"] || !s.Filters["localhost"] {
							return fmt.Errorf("filters = %v", s.Filters)
						}
						return nil
					},
				),
			},
			{
				Config:           config(true, `"safari", "ie"`), // order does not matter
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				ResourceName:      "sentry_project_inbound_filter.crawlers",
				ImportState:       true,
				ImportStateId:     "api/web-crawlers",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "sentry_project_inbound_filter.legacy",
				ImportState:       true,
				ImportStateId:     "api/legacy-browsers",
				ImportStateVerify: true,
			},
			{
				Config: config(false, `"ie", "edge", "opera"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_inbound_filter.crawlers", "active", "false"),
					browsersAre("ie", "edge", "opera"),
				),
			},
			{
				// Changed in the UI: drift is corrected.
				PreConfig: func() {
					f.mutate(func() {
						pr := f.project(p.ID)
						pr.Filters["web-crawlers"] = true
						pr.LegacyBrowsers = []string{"chrome"}
					})
				},
				Config: config(false, `"ie", "edge", "opera"`),
				Check:  browsersAre("ie", "edge", "opera"),
			},
			{
				// An empty set turns legacy-browsers off and settles.
				Config: config(false, ``),
				Check:  browsersAre(),
			},
			{
				Config:           config(false, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// Sentry drops browsers it does not know.
				Config:      config(false, `"ie", "netscape"`),
				ExpectError: regexp.MustCompile(`Sentry stored different subfilters`),
			},
			{
				Config: config(false, `"ie"`),
			},
		},
	})
}

func TestProjectInboundFilterValidation(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))
	one := func(body string) string {
		return f.providerConfig() + fmt.Sprintf("resource \"sentry_project_inbound_filter\" \"x\" {\n  project_id = %q\n%s\n}\n", p.ID, body)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      one("  filter_id  = \"legacy-browsers\"\n  active     = true"),
				ExpectError: regexp.MustCompile(`(?s)Missing subfilters.*no\s+` + "`active`" + `\s+switch`),
			},
			{
				Config:      one("  filter_id  = \"localhost\"\n  subfilters = [\"ie\"]"),
				ExpectError: regexp.MustCompile(`(?s)Missing active.*Only the`),
			},
			{
				Config:      one("  filter_id = \"no-such-filter\"\n  active    = true"),
				ExpectError: regexp.MustCompile(`Unknown inbound filter`),
			},
			{
				Config: one("  filter_id = \"localhost\"\n  active    = true"),
			},
		},
	})
}
