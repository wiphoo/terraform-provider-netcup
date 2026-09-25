# Restore a server from a snapshot in Terraform.
#
# WARNING: Creating or replacing this resource REVERTS the server to the
# specified snapshot, discarding all changes made since the snapshot, and
# REBOOTS the server — data loss is possible. Destroying the resource is a
# no-op: it only removes Terraform state and does NOT revert the server.
#
# This example is opt-in. Set restore_enabled=true and provide a real server
# ID plus an exact snapshot name before applying:
#
#   terraform plan -var 'restore_server_id=123456' -var 'restore_enabled=true' \
#     -var 'restore_snapshot_name=pre-upgrade'

variable "restore_enabled" {
  description = "Set to true to enable the destructive restore example."
  type        = bool
  default     = false
}

variable "restore_server_id" {
  description = "Numeric netcup server ID to restore. null (default) skips the resource."
  type        = string
  default     = null
}

variable "restore_snapshot_name" {
  description = "Name of the snapshot in SCP to restore to."
  type        = string
  default     = null
}

resource "netcup_server_snapshot_restore" "example" {
  count             = var.restore_server_id != null && var.restore_enabled ? 1 : 0
  server_id         = var.restore_server_id
  snapshot_name     = var.restore_snapshot_name

  # Keep the example synchronous by default. Set wait=false when callers only
  # need the API acceptance and will monitor the task separately.
  wait = true
}

output "restore_id" {
  description = "The ID of the initiated restore operation, or null when disabled."
  value       = try(one(netcup_server_snapshot_restore.example).id, null)
}