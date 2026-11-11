# Applies to every project of the organization. Declare it once.
resource "sentry_organization_data_scrubbing" "this" {
  data_scrubber          = true
  data_scrubber_defaults = true

  safe_fields = ["request_id"]
}
