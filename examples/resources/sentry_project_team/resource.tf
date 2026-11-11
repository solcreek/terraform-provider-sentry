resource "sentry_team" "sre" {
  name = "SRE"
}

# Give a second team access to a project. The project's own team is set by
# sentry_project.team_id.
resource "sentry_project_team" "api_sre" {
  project_id = sentry_project.api.id
  team_id    = sentry_team.sre.id
}
