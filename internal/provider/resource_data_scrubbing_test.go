// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestProjectDataScrubbingLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	p := f.addProject("API", f.addTeam("Backend"))

	config := func(body string) string {
		return f.providerConfig() + fmt.Sprintf("resource \"sentry_project_data_scrubbing\" \"api\" {\n  project_id = %q\n%s\n}\n", p.ID, body)
	}
	fields := `  safe_fields      = ["request_id", "build"]` + "\n" + `  sensitive_fields = ["session_token"]`
	scrubbing := func() fakeScrubbing { return f.snapshotProject(p.ID).Scrubbing }
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Destroying must not switch anything off or clear the lists.
			s := scrubbing()
			if !s.DataScrubber || !s.ScrubIPAddresses || len(s.SafeFields) != 1 {
				return fmt.Errorf("destroy changed the settings: %+v", s)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Only the lists are set: the switches keep Sentry's values.
				Config: config(fields),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.api", "id", p.ID),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.api", "data_scrubber", "true"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.api", "data_scrubber_defaults", "true"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.api", "scrub_ip_addresses", "false"),
					resource.TestCheckResourceAttr("sentry_project_data_scrubbing.api", "safe_fields.#", "2"),
					func(*terraform.State) error {
						s := scrubbing()
						slices.Sort(s.SafeFields)
						if !slices.Equal(s.SafeFields, []string{"build", "request_id"}) || !slices.Equal(s.SensitiveFields, []string{"session_token"}) {
							return fmt.Errorf("scrubbing = %+v", s)
						}
						return nil
					},
				),
			},
			{Config: config(fields), ConfigPlanChecks: empty},
			{ResourceName: "sentry_project_data_scrubbing.api", ImportState: true, ImportStateId: "api", ImportStateVerify: true},
			{
				Config: config("  scrub_ip_addresses = true\n  safe_fields        = [\"request_id\"]\n  sensitive_fields   = []"),
				Check: func(*terraform.State) error {
					s := scrubbing()
					if !s.ScrubIPAddresses || !s.DataScrubber || len(s.SafeFields) != 1 || len(s.SensitiveFields) != 0 {
						return fmt.Errorf("scrubbing = %+v", s)
					}
					return nil
				},
			},
			{Config: config("  scrub_ip_addresses = true\n  safe_fields        = [\"request_id\"]\n  sensitive_fields   = []"), ConfigPlanChecks: empty},
			{
				// Switched off in the UI: drift on a managed setting is corrected.
				PreConfig: func() { f.mutate(func() { f.project(p.ID).Scrubbing.ScrubIPAddresses = false }) },
				Config:    config("  scrub_ip_addresses = true\n  safe_fields        = [\"request_id\"]\n  sensitive_fields   = []"),
				Check: func(*terraform.State) error {
					if !scrubbing().ScrubIPAddresses {
						return fmt.Errorf("drift was not corrected")
					}
					return nil
				},
			},
			{
				// A setting removed from the configuration is no longer managed
				// and keeps its value.
				Config:           config("  scrub_ip_addresses = true"),
				ConfigPlanChecks: empty,
			},
		},
	})
}

func TestOrganizationDataScrubbingLifecycle(t *testing.T) {
	f := newFakeSentry(t)
	config := func(body string) string {
		return f.providerConfig() + "resource \"sentry_organization_data_scrubbing\" \"this\" {\n" + body + "\n}\n"
	}
	org := func() fakeScrubbing {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.orgScrubbing
	}
	on := "  data_scrubber          = true\n  data_scrubber_defaults = true\n  safe_fields            = [\"request_id\"]"
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if s := org(); !s.DataScrubber || len(s.SafeFields) != 1 {
				return fmt.Errorf("destroy changed the settings: %+v", s)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(on),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("sentry_organization_data_scrubbing.this", "id", fakeOrgID),
					resource.TestCheckResourceAttr("sentry_organization_data_scrubbing.this", "scrub_ip_addresses", "false"),
					resource.TestCheckResourceAttr("sentry_organization_data_scrubbing.this", "sensitive_fields.#", "0"),
					func(*terraform.State) error {
						if s := org(); !s.DataScrubber || !s.DataScrubberDefaults || s.ScrubIPAddresses {
							return fmt.Errorf("scrubbing = %+v", s)
						}
						return nil
					},
				),
			},
			{Config: config(on), ConfigPlanChecks: empty},
			{ResourceName: "sentry_organization_data_scrubbing.this", ImportState: true, ImportStateId: "anything", ImportStateVerify: true},
			{
				Config: config(on + "\n  scrub_ip_addresses     = true"),
				Check: func(*terraform.State) error {
					if !org().ScrubIPAddresses {
						return fmt.Errorf("scrub_ip_addresses was not set")
					}
					return nil
				},
			},
		},
	})
}
