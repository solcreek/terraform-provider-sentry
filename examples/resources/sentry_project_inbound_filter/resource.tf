resource "sentry_project_inbound_filter" "crawlers" {
  project_id = sentry_project.api.id
  filter_id  = "web-crawlers"
  active     = true
}

# The legacy browsers filter takes the browsers to filter instead of a switch.
resource "sentry_project_inbound_filter" "legacy_browsers" {
  project_id = sentry_project.api.id
  filter_id  = "legacy-browsers"
  subfilters = ["ie", "opera_mini"]
}
