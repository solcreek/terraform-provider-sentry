resource "sentry_project_data_scrubbing" "api" {
  project_id = sentry_project.api.id

  data_scrubber          = true
  data_scrubber_defaults = true
  scrub_ip_addresses     = true

  # Never scrub these, even if they look sensitive.
  safe_fields = ["request_id", "build_number"]

  # Scrub these on top of Sentry's defaults.
  sensitive_fields = ["session_token", "internal_account_ref"]
}
