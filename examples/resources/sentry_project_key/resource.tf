resource "sentry_project_key" "web" {
  project_id = sentry_project.api.id
  name       = "Web"

  # Optional: accept at most 1000 events per hour with this key.
  rate_limit = {
    window = 3600
    count  = 1000
  }
}

output "sentry_dsn" {
  value = sentry_project_key.web.dsn_public
}
