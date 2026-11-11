resource "sentry_team" "backend" {
  name = "Backend"
}

resource "sentry_project" "api" {
  name     = "API"
  platform = "python-django"
  team_id  = sentry_team.backend.id

  # Skip the alert Sentry would otherwise create with the project.
  default_rules = false

  # Projects are protected by default. To delete one, first apply:
  # deletion_protection = false
}
