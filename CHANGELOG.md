# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-11-11

### Fixed

- `sentry_project_key` and `sentry_project_team` now work with a project slug as
  `project_id`. The key failed with "inconsistent result after apply" and the team
  access planned a replacement on every run. The slug is kept as written.

### Changed

- Every `project_id` argument documents that it takes an ID or a slug, and that a slug
  stops resolving when the project is renamed.

## [0.1.0] - 2026-11-11

### Added

- `sentry_team` resource: a team, tracked by its numeric ID so that renaming it
  or changing its slug is an in-place update. Import by slug or ID.
- `sentry_project` resource: a project owned by a team (`team_id`), with
  `platform` and the creation-only `default_rules`. Moving it to another team
  grants the new team access before revoking the old one's.
  `deletion_protection` defaults to `true`. Import by slug or ID.
- `sentry_project_team` resource: gives one more team access to a project.
  Import by `<project>/<team>`, each a slug or ID.
- `sentry_project_key` resource: a client key with an optional rate limit and
  `is_active`; exports the DSNs and the other ingestion endpoints. Import by
  `<project>/<key_id>`. Where the organization's plan has no key rate
  limits, Sentry would drop `rate_limit` silently; the provider checks the
  project's `rate-limits` feature first and refuses before creating or
  changing the key.
- `sentry_project_ownership` resource: a project's issue ownership rules,
  fallthrough and auto-assignment. Destroying it clears the rules. Import by
  project slug or ID.
- `sentry_project_inbound_filter` resource: one of a project's built-in
  inbound data filters, including the browser list of `legacy-browsers`.
  Destroying it turns the filter off. Import by `<project>/<filter_id>`.
- `sentry_organization_member` resource: invites an email address with an
  organization role and removes the member, or withdraws the invitation, on
  destroy. An address that is already a member is refused, and a role change
  keeps the member's teams. Import by email or member ID.
- `sentry_team_member` resource: puts an organization member on a team.
  Import by `<team>/<member_id>`.
- `sentry_project_data_scrubbing` and `sentry_organization_data_scrubbing`
  resources: the data scrubbers, IP address scrubbing and the sensitive and
  safe field lists, per project and organization-wide. Unset settings keep
  Sentry's value, and destroying the resource leaves the settings in place.
- `sentry_organization`, `sentry_organization_member`, `sentry_team`,
  `sentry_project` and `sentry_project_key` data sources.
- Dependency-free Sentry API client: bearer-token auth, sentry.io, region and
  self-hosted base URLs, `Link`-header pagination, a cap on concurrent
  requests, rate-limit back-off from `Retry-After` and
  `X-Sentry-Rate-Limit-Reset`, retries of reads after a transport error, and
  actionable credential errors.

[unreleased]: https://github.com/solcreek/terraform-provider-sentry/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/solcreek/terraform-provider-sentry/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/solcreek/terraform-provider-sentry/releases/tag/v0.1.0
