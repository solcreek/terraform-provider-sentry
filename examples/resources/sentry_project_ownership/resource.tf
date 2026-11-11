resource "sentry_project_ownership" "api" {
  project_id = sentry_project.api.id

  raw = <<-EOT
    path:src/checkout/* #payments
    tags.transaction:/api/billing/* #payments
    url:https://example.com/admin/* jane@example.com
  EOT

  fallthrough     = false
  auto_assignment = "Auto Assign to Issue Owner"
}
