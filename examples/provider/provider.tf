terraform {
  required_providers {
    sentry = {
      source = "registry.opentofu.org/solcreek/sentry"
    }
  }
}

provider "sentry" {
  # The auth token is read from the SENTRY_AUTH_TOKEN environment variable by
  # default; it can also be set explicitly here.
  # auth_token = "..."

  # Slug of the organization to manage; or the SENTRY_ORG environment variable.
  organization = "acme"

  # Optional: a self-hosted install or a region host.
  # base_url = "https://sentry.example.com"
}
