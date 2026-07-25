# Manage the rescue system for a server.
#
# WARNING: Enabling rescue mode REBOOTS the server into the rescue environment
# (causes downtime). Disabling rescue mode REBOOTS the server back into its
# normal operating system (also causes downtime). Both enable and disable are
# asynchronous operations; the resource waits for each to complete by default.
#
# This example is opt-in: it is skipped unless you supply a real server ID, so
# a bare `terraform plan` does not fail on an account that lacks a placeholder
# server. Enable it with:
#
#   terraform plan -var 'server_id=123456'
#
# The resource reads back the rescue password once activation completes (the
# password is marked Sensitive in the schema and never logged).
#
# Destroying this resource disables rescue mode (another reboot).
resource "netcup_server_rescue" "example" {
  count     = var.server_id == null ? 0 : 1
  server_id = var.server_id
}

output "rescue_active" {
  description = "Whether the rescue system is active (null when server_id is unset)."
  value       = try(one(netcup_server_rescue.example).active, null)
}

output "rescue_password" {
  description = "The rescue system password (sensitive, null when not available)."
  value       = try(one(netcup_server_rescue.example).password, null)
  sensitive   = true
}
