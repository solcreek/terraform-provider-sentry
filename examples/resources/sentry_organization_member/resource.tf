# Sends an invitation by email.
resource "sentry_organization_member" "jane" {
  email = "jane@example.com"
  role  = "member"
}
