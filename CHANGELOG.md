# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
  limits, Sentry drops `rate_limit` silently; the provider reports that as an
  error.
- `sentry_project_ownership` resource: a project's issue ownership rules,
  fallthrough and auto-assignment. Destroying it clears the rules. Import by
  project slug or ID.
- `sentry_organization`, `sentry_team`, `sentry_project` and
  `sentry_project_key` data sources.
- Dependency-free Sentry API client: bearer-token auth, sentry.io, region and
  self-hosted base URLs, `Link`-header pagination, a cap on concurrent
  requests, rate-limit back-off from `Retry-After` and
  `X-Sentry-Rate-Limit-Reset`, and actionable credential errors.

[unreleased]: https://github.com/solcreek/terraform-provider-sentry/commits/main
