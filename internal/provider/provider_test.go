// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestMain serves the in-process provider under the solcreek namespace, which
// the test configurations name in required_providers, so the same tests run
// under Terraform and OpenTofu (TF_ACC_TERRAFORM_PATH=$(command -v tofu)).
func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC_PROVIDER_NAMESPACE") == "" {
		_ = os.Setenv("TF_ACC_PROVIDER_NAMESPACE", "solcreek")
	}
	if os.Getenv("TF_ACC_PROVIDER_HOST") == "" && filepath.Base(os.Getenv("TF_ACC_TERRAFORM_PATH")) == "tofu" {
		_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	}
	os.Exit(m.Run())
}

// requiredProviders pins the provider source in every test configuration.
const requiredProviders = `
terraform {
  required_providers {
    sentry = {
      source = "solcreek/sentry"
    }
  }
}
`

// testProtoV6ProviderFactories wires the in-process provider for tests.
var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"sentry": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck verifies the environment for acceptance tests, which make
// real API calls and require:
//   - TF_ACC=1
//   - SENTRY_AUTH_TOKEN  a token with org:read, team:admin and project:admin
//   - SENTRY_TEST_ORG    slug of a dedicated organization the tests may write to
//
// The organization comes from SENTRY_TEST_ORG, never SENTRY_ORG, so the tests
// cannot land in an everyday organization by accident. Without TF_ACC the
// test is skipped.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests are skipped unless TF_ACC is set")
	}
	if os.Getenv("SENTRY_AUTH_TOKEN") == "" {
		t.Fatal("SENTRY_AUTH_TOKEN must be set for acceptance tests")
	}
	if os.Getenv("SENTRY_TEST_ORG") == "" {
		t.Fatal("SENTRY_TEST_ORG must be set for acceptance tests")
	}
}

func TestProviderMissingCredentials(t *testing.T) {
	newFakeSentry(t) // clears SENTRY_* from the environment
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      requiredProviders + `data "sentry_organization" "this" {}`,
			ExpectError: regexp.MustCompile(`(?s)Missing Sentry credentials.*Missing Sentry organization`),
		}},
	})
}

func TestProviderInvalidToken(t *testing.T) {
	f := newFakeSentry(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `
provider "sentry" {
  auth_token   = "wrong"
  organization = "` + fakeOrgSlug + `"
  base_url     = "` + f.srv.URL + `"
}
data "sentry_organization" "this" {}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable to read organization.*invalid,\s+expired\s+or\s+revoked`),
		}},
	})
}

func TestProviderEnvironment(t *testing.T) {
	f := newFakeSentry(t)
	t.Setenv("SENTRY_AUTH_TOKEN", fakeToken)
	t.Setenv("SENTRY_ORG", fakeOrgSlug)
	t.Setenv("SENTRY_BASE_URL", f.srv.URL+"/api/0/") // the API suffix is tolerated
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `data "sentry_organization" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.sentry_organization.this", "id", fakeOrgID),
				resource.TestCheckResourceAttr("data.sentry_organization.this", "slug", fakeOrgSlug),
				resource.TestCheckResourceAttr("data.sentry_organization.this", "name", "Acme Inc"),
			),
		}},
	})
}

// randomSuffix returns a short random hex string for acceptance test names.
func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
