<a href="https://terraform.io">
    <img src=".github/tf.svg" alt="Terraform logo" title="Terraform" align="left" height="50" />
</a>
<a href="https://opentofu.org">
    <picture>
        <source media="(prefers-color-scheme: dark)" srcset=".github/opentofu-dark.svg" />
        <img src=".github/opentofu-light.svg" alt="OpenTofu logo" title="OpenTofu" align="left" height="50" />
    </picture>
</a>


# Terraform / OpenTofu Provider for Sentry

[![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/solcreek/terraform-provider-sentry?label=release&style=for-the-badge)](https://github.com/solcreek/terraform-provider-sentry/releases/latest) [![License](https://img.shields.io/github/license/solcreek/terraform-provider-sentry.svg?style=for-the-badge)](LICENSE) [![Tests](https://img.shields.io/github/actions/workflow/status/solcreek/terraform-provider-sentry/test.yml?branch=main&style=for-the-badge)](https://github.com/solcreek/terraform-provider-sentry/actions)

A provider for [Sentry](https://sentry.io): teams, projects, team access to
projects and client keys (DSNs), on sentry.io or a self-hosted install. It uses a
**dependency-free, standard-library-only** client written against the
[Sentry API reference](https://docs.sentry.io/api/), so the provider owns its
HTTP behaviour: it caps concurrent requests, backs off when rate limited, follows
pagination, and turns credential errors into actionable messages.

> [!NOTE]
> This is an **unofficial, community-maintained** provider. It is **not**
> affiliated with, endorsed by, or supported by Sentry. "Sentry" is a trademark
> of its respective owner and is used here only to describe what the provider
> integrates with.

> [!WARNING]
> **Pre-release.** Every resource and data source passed its acceptance test
> against sentry.io on 2026-11-11, with two gaps: a client key `rate_limit` that
> takes effect, which needs a Sentry plan the test organization does not have,
> and `sentry_organization_data_scrubbing`, whose test would change a real
> organization's settings and has only run against an in-memory model.
> Nothing has been released yet, and self-hosted Sentry is untested.

Proudly built for the open-source IaC ecosystem and dedicated to **OpenTofu**.

## Usage

> [!IMPORTANT]
> The provider is meant for the **OpenTofu Registry**, so always use the
> **full** `registry.opentofu.org/solcreek/sentry` address. It works with both
> OpenTofu and Terraform. The short `solcreek/sentry` form only works with
> OpenTofu: Terraform resolves short addresses against `registry.terraform.io`.

```hcl
terraform {
  required_providers {
    sentry = {
      source = "registry.opentofu.org/solcreek/sentry"
    }
  }
}

provider "sentry" {
  # auth_token = "..."  # or the SENTRY_AUTH_TOKEN environment variable
  organization = "acme" # or the SENTRY_ORG environment variable
}

resource "sentry_team" "backend" {
  name = "Backend"
}

resource "sentry_project" "api" {
  name          = "API"
  platform      = "python-django"
  team_id       = sentry_team.backend.id
  default_rules = false
}

resource "sentry_project_key" "api" {
  project_id = sentry_project.api.id
  name       = "Production"
}

output "sentry_dsn" {
  value = sentry_project_key.api.dsn_public
}
```

See [`examples/`](./examples) for every resource and data source.

## Resources & data sources

| Kind | Name | Purpose |
|------|------|---------|
| resource | `sentry_team` | A team. Renames are in place. |
| resource | `sentry_project` | A project owned by a team. Deletion protected by default. |
| resource | `sentry_project_team` | Give one more team access to a project. |
| resource | `sentry_project_key` | A client key: DSNs, rate limit, active or not. |
| resource | `sentry_project_ownership` | A project's issue ownership rules and auto-assignment. |
| resource | `sentry_project_inbound_filter` | Turn a built-in inbound data filter on or off. |
| resource | `sentry_organization_member` | Invite a member with an organization role. |
| resource | `sentry_team_member` | Put an organization member on a team. |
| resource | `sentry_project_data_scrubbing` | A project's data scrubbers and sensitive / safe field lists. |
| resource | `sentry_organization_data_scrubbing` | The same settings organization-wide. |
| data | `sentry_organization` | The organization the provider is configured for. |
| data | `sentry_organization_member` | Look up a member by email address. |
| data | `sentry_team` | Look up a team by slug or ID. |
| data | `sentry_project` | Look up a project by slug or ID, with the teams that have access. |
| data | `sentry_project_key` | Look up a client key, e.g. the `Default` one, to read its DSN. |

All resources support `terraform import`.

## Authentication

Create an [internal integration](https://docs.sentry.io/organization/integrations/integration-platform/#internal-integrations)
in the organization's settings and supply its token with the `auth_token`
argument or the `SENTRY_AUTH_TOKEN` environment variable. A personal token works
too. Managing everything here needs the `org:read`, `team:admin`,
`project:admin` and, for members, `member:admin`
[scopes](https://docs.sentry.io/api/permissions/). Sentry only lets a personal
token change the organization role of an existing member.

`organization` (or `SENTRY_ORG`) names the one organization the provider
manages. `base_url` (or `SENTRY_BASE_URL`) points it at a self-hosted install or
a region host such as `https://de.sentry.io`; it defaults to `https://sentry.io`.

What happens with bad credentials:

- **No token or organization** → the provider fails at configuration time with
  *"Missing Sentry credentials"* / *"Missing Sentry organization"*.
- **Invalid, expired or revoked token** → the API returns `401`, surfaced as such.
- **Missing scope** → the API returns `403`, surfaced as a hint that the token
  lacks a scope the call needs.

## Behaviour worth knowing

- **IDs, not slugs.** Sentry's slugs change when someone renames a team or
  project. Resources are tracked by numeric ID and reference each other by `id`,
  so a rename, in Terraform or in the Sentry UI, is an in-place update and never
  a replacement. When `slug` is unset, Sentry derives it from the name once, at
  creation, adding a short suffix if that slug is taken; an explicit `slug`
  that is taken is an error.
- **Deleting a project is guarded.** `sentry_project` defaults to
  `deletion_protection = true`: a delete removes the project's events and Sentry
  has no undo. Apply `deletion_protection = false` first.
- **Deletes are scheduled.** Sentry deletes teams and projects asynchronously;
  a destroy returns once the deletion is scheduled. They disappear from the API
  and free their slug at once.
- **Client key rate limits depend on the plan.** Without the project's
  `rate-limits` feature, Sentry accepts `rate_limit` and silently drops it. The
  provider checks the feature first and refuses before it creates or changes
  the key, so a key is never left tainted and its DSN never churns.
- **Project teams.** `sentry_project.team_id` is the team the project is created
  under; changing it grants the new team access and then revokes the old one's.
  Other teams get access through `sentry_project_team`. A team whose access was
  revoked in the UI shows up as drift and is restored.
- **Settings that always exist.** A project always has an ownership
  configuration and every inbound filter. `sentry_project_ownership` and
  `sentry_project_inbound_filter` take them over when created; destroying them
  clears the rules or turns the filter off rather than deleting anything.
- **Data scrubbing is never reset.** `sentry_project_data_scrubbing` and
  `sentry_organization_data_scrubbing` only change the settings the
  configuration names, and destroying them leaves everything as it is, because
  a reset could switch scrubbing off. Field names are stored as written.
- **Members are invited by email.** Creating `sentry_organization_member`
  sends an invitation; `pending` tells whether it was accepted, and a pending
  member can already be put on teams. An address that is already a member is
  refused rather than adopted: import it instead. Team membership is separate
  (`sentry_team_member`), survives a change of organization role, and team
  roles are not managed yet.
- **Team creators join their team.** A team created with a personal token
  starts with that token's user as a member. Declaring the same membership
  with `sentry_team_member` then fails with a hint to import it.
- **`default_rules` is creation-only.** It controls the alert Sentry creates
  with a new project. Sentry does not report it, so it is not compared later.
- **Secrets live in the state.** `secret_key` and `dsn_secret` are marked
  sensitive, which hides them from plan output only.
- **Rate limits.** Requests are capped at four in flight; a `429` is retried
  after `Retry-After` or the window reset Sentry reports, at most five times.
  A read that fails on the network is repeated up to twice; a write never is.

## Development

```sh
make build        # compile the provider
make test         # unit and lifecycle tests against the in-memory API (needs terraform on PATH)
make test-tofu    # the same lifecycle tests through OpenTofu
make generate     # regenerate docs/ from schema, examples/ and templates/
make lint         # golangci-lint
```

Acceptance tests (`make testacc`) call the real Sentry API and create and delete
teams and projects. They need `SENTRY_AUTH_TOKEN` and `SENTRY_TEST_ORG`, the
slug of a **dedicated** organization the tests may write to.

## License

[MPL-2.0](LICENSE). See [NOTICE](NOTICE).
