data "sentry_organization_member" "jane" {
  email = "jane@example.com"
}

resource "sentry_team_member" "backend_jane" {
  team_id   = sentry_team.backend.id
  member_id = data.sentry_organization_member.jane.id
}
