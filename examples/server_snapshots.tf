# List all snapshots for a specific server.
#
# This example is opt-in: it is skipped unless you supply a real server ID, so
# a bare `terraform plan` does not fail on an account that lacks a placeholder
# server. Enable it with:
#
#   terraform plan -var 'server_id=123456'

data "netcup_server_snapshots" "example" {
  count     = var.server_id == null ? 0 : 1
  server_id = var.server_id
}

output "snapshots" {
  description = "All snapshots for the server (null when server_id is unset)."
  value       = one(data.netcup_server_snapshots.example[*].snapshots)
}
