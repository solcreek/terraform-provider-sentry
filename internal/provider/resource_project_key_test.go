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

func TestProjectKeyLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))

	config := func(body string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "sentry_project_key" "web" {
  project_id = %q
%s
}
`, p.ID, body)
	}
	limited := `
  name = "Web"
  rate_limit = {
    window = 60
    count  = 100
  }`

	var id string
	key := func() *fakeKey {
		for _, k := range f.snapshotProject(p.ID).Keys {
			if k.ID == id {
				return k
			}
		}
		return nil
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if key() != nil {
				return fmt.Errorf("client key %s still exists", id)
			}
			if n := len(f.snapshotProject(p.ID).Keys); n != 1 {
				return fmt.Errorf("project has %d keys, want only the Default one", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(limited),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("sentry_project_key.web", "id", func(v string) error {
						id = v
						if key() == nil {
							return fmt.Errorf("no key with id %s", v)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("sentry_project_key.web", "is_active", "true"),
					resource.TestCheckResourceAttr("sentry_project_key.web", "rate_limit.window", "60"),
					resource.TestCheckResourceAttr("sentry_project_key.web", "rate_limit.count", "100"),
					resource.TestMatchResourceAttr("sentry_project_key.web", "dsn_public", regexp.MustCompile(`^https://pub\d+@o1\.ingest\.sentry\.test/`+p.ID+`$`)),
					resource.TestMatchResourceAttr("sentry_project_key.web", "dsn_secret", regexp.MustCompile(`^https://pub\d+:sec\d+@`)),
					resource.TestCheckResourceAttrSet("sentry_project_key.web", "secret_key"),
					// The two DSNs have their own attributes; the rest is in the map.
					resource.TestCheckResourceAttr("sentry_project_key.web", "endpoints.%", "2"),
					resource.TestCheckResourceAttrSet("sentry_project_key.web", "endpoints.csp"),
					resource.TestCheckResourceAttrSet("sentry_project_key.web", "endpoints.otlp_traces"),
				),
			},
			{
				Config: config(limited),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      "sentry_project_key.web",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return "api/" + id, nil },
				ImportStateVerify: true,
			},
			{
				// Rename, deactivate and lift the rate limit in place.
				Config: config("\n  name      = \"Web (old)\"\n  is_active = false"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("sentry_project_key.web", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("sentry_project_key.web", "rate_limit.window"),
					func(*terraform.State) error {
						k := key()
						if k.Name != "Web (old)" || k.IsActive || k.RateLimit != nil {
							return fmt.Errorf("key = %+v", *k)
						}
						return nil
					},
				),
			},
			{
				// Reactivated in the UI: drift is corrected.
				PreConfig: func() { f.mutate(func() { f.project(p.ID).Keys[1].IsActive = true }) },
				Config:    config("\n  name      = \"Web (old)\"\n  is_active = false"),
				Check: func(*terraform.State) error {
					if key().IsActive {
						return fmt.Errorf("key is still active")
					}
					return nil
				},
			},
		},
	})
}

func TestProjectKeyCreatedInactive(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + fmt.Sprintf(`
resource "sentry_project_key" "spare" {
  project_id = %q
  name       = "Spare"
  is_active  = false
}
`, p.ID),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sentry_project_key.spare", "is_active", "false"),
				func(*terraform.State) error {
					if k := f.snapshotProject(p.ID).Keys[1]; k.IsActive {
						return fmt.Errorf("key was left active")
					}
					return nil
				},
			),
		}},
	})
}
