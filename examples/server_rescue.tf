# Manage the rescue system for a server.
#
# WARNING: Enabling rescue mode REBOOTS the server into the rescue environment
# (causes downtime). Disabling rescue mode REBOOTS the server back into its
# normal operating system (also causes downtime). Both enable and disable are
# asynchronous operations; the resource waits for each to complete by default.
#
# This example is opt-in TWICE: you must supply both a real server ID AND
# set rescue_enable to true, so a bare `terraform plan` and other examples
# that only pass -var 'server_id=...' cannot accidentally schedule this
# disruptive operation.
#
# ⚠️ IMPORTANT: Once applied with rescue_enable=true, a subsequent plan
# THAT OMITS rescue_enable (or sets it to false) changes count from 1 to 0,
# which schedules DESTRUCTION of the rescue resource and TRIGGERS ANOTHER
# REBOOT to disable rescue mode. Keep rescue_enable=true in your
# configuration for any server that should remain in rescue mode, or
# explicitly accept the reboot when removing it.
#
# Enable it with:
#
#   terraform plan -var 'server_id=123456' -var 'rescue_enable=true'
#
# The resource reads back the rescue password once activation completes (the
# password is marked Sensitive in the schema and never logged).
#
# Destroying this resource disables rescue mode (another reboot).
variable "rescue_enable" {
  description = "Set to true to enable the rescue system example. Defaults to false to prevent accidental reboot."
  type        = bool
  default     = false
}

resource "netcup_server_rescue" "example" {
  count     = var.server_id != null && var.rescue_enable ? 1 : 0
  server_id = var.server_id
}

output "rescue_active" {
  description = "Whether the rescue system is active (null when unset or rescue_enable is false)."
  value       = try(one(netcup_server_rescue.example).active, null)
}

output "rescue_password" {
  description = "The rescue system password (sensitive, null when not available)."
  value       = try(one(netcup_server_rescue.example).password, null)
  sensitive   = true
}
