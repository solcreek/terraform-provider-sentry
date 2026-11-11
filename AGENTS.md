# Contributor instructions

- Write all repository documentation, comments, examples, and diagnostics in US English.
- License original source under MPL-2.0. Preserve dependency licenses and notices, and review license compatibility before adding code or dependencies.
- Keep the Sentry API client (`internal/sentry`) standard-library only. Implement against Sentry's published [API reference](https://docs.sentry.io/api/) and OpenAPI schema (`getsentry/sentry-api-schema`); where the live API differs from them, follow the live API and say so in a comment. Do not vendor the schema or generate code from it (it is FSL-licensed), and do not copy community provider implementations.
- Only use the current Monitors and Alerts API (detectors, workflows) for alerting; do not build on the legacy issue-alert or metric-alert endpoints. Skip operations the schema marks experimental.
- Identify teams and projects by numeric ID in state and in API paths. Slugs are mutable attributes, so a rename is an in-place update, never a replacement.
- Follow Terraform Plugin Framework plan/state consistency, import, and diagnostic conventions. Every resource needs a lifecycle test against the in-memory fake API (`internal/provider/fake_sentry_test.go`), and the fake must keep mirroring observed live behavior: when `TestAccAPIFacts` shows a difference, change the fake and its dated comment.
- Destructive operations stay guarded: projects keep `deletion_protection = true` by default. A resource must never adopt something it did not create (an existing member, team access or membership): refuse and point to `terraform import`.
- Never expose credentials in diagnostics, logs, example files, or URLs. Mark secrets `Sensitive`; they still persist in state.
- Keep commits atomic and commit completed, verified work frequently. Run `make test` (and `make test-tofu`) before each code commit, and `make generate` when schema, examples or templates change.
- Do not claim live Sentry verification from local fixtures. Real API tests require explicit opt-in (`TF_ACC`, `SENTRY_AUTH_TOKEN`, `SENTRY_TEST_ORG`) and an organization whose owner agreed to the tests writing to it; they only create and delete `tf-acc-*` teams and projects. `TestAccTeamMember` needs `SENTRY_TEST_MEMBER_ID`, and `TestAccOrganizationMember` needs `SENTRY_TEST_INVITE_EMAIL` (an address that is not a member; Sentry emails it).
- Follow the Solcreek Gandi provider's OpenTofu Registry release layout. Preserve published version tags and the registered Solcreek signing identity.
