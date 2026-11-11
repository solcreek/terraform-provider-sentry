# The key Sentry creates with every project.
data "sentry_project_key" "default" {
  project = "api"
  name    = "Default"
}

output "sentry_dsn" {
  value = data.sentry_project_key.default.dsn_public
}
