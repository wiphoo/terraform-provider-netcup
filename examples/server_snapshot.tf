# Manage a server snapshot in Terraform.
#
# Every input is immutable (any change replaces the snapshot – the old one is deleted and a
# new one is taken). Destroying the resource DELETES the snapshot.
#
# Offline/online rules (same as `netcupctl server snapshots create`):
#   - offline (default): set disk_name to the disk to snapshot
#   - online:            set online_snapshot = true and leave disk_name unset
#
# Adopt an existing snapshot instead of creating one with:
#   terraform import 'netcup_server_snapshot.example[0]' '<server_id>:<snapshot_name>'

variable "server_id" {
  type        = string
  default     = null
  description = "Numeric netcup server ID to snapshot. null (default) skips the resource."
}

variable "snapshot_name" {
  type        = string
  default     = "example-snapshot"
  description = "Name of the snapshot in SCP (max 255 characters)."
}

variable "disk_name" {
  type        = string
  default     = ""
  description = "Disk to snapshot for an offline snapshot. Empty (default) requires online_snapshot = true."
}

variable "online_snapshot" {
  type        = bool
  default     = false
  description = "Take an online snapshot of the running server (mutually exclusive with disk_name)."
}

resource "netcup_server_snapshot" "example" {
  count           = var.server_id == null ? 0 : 1
  server_id       = var.server_id
  name            = var.snapshot_name
  description     = "Created by the server_snapshot example"
  disk_name       = var.disk_name != "" ? var.disk_name : null
  online_snapshot = var.online_snapshot

  # Keep the example synchronous by default. Set wait=false when callers only
  # need the API acceptance and will monitor the task separately.
  wait = true
}

output "snapshot_id" {
  description = "The ID of the created snapshot, or null when disabled."
  value       = try(one(netcup_server_snapshot.example).id, null)
}
