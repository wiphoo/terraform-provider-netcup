# Manage a server's desired power state.
#
# WARNING: Setting state to OFF or SUSPENDED causes immediate downtime.
# Destroying this resource is a no-op — it does NOT power the server off
# (state-only removal).
#
# This example is opt-in: it is skipped unless you supply a real server ID
# and a desired power state, so a bare `terraform plan` does not fail on an
# account that lacks a placeholder server. Enable it with:
#
#   terraform plan -var 'server_id=123456' -var 'power_state=ON'
#
# To set a specific state:
#
#   terraform apply -var 'server_id=123456' -var 'power_state=ON'
#   terraform apply -var 'server_id=123456' -var 'power_state=OFF'
#   terraform apply -var 'server_id=123456' -var 'power_state=SUSPENDED'
#
# Adding -var 'power_wait=false' returns immediately after the API accepts the
# command without polling the async task to completion.
variable "power_state" {
  description = "Desired power state (ON, OFF, or SUSPENDED). Leave null to skip this example."
  type        = string
  default     = null

  validation {
    condition     = var.power_state == null ? true : contains(["ON", "OFF", "SUSPENDED"], var.power_state)
    error_message = "power_state must be one of: ON, OFF, SUSPENDED."
  }
}

variable "power_wait" {
  description = "Whether to wait for the async power task to complete (defaults to true)."
  type        = bool
  default     = true
}

resource "netcup_server_power" "example" {
  count      = var.server_id != null && var.power_state != null ? 1 : 0
  server_id  = var.server_id
  state      = var.power_state
  wait       = var.power_wait
}

output "power_state" {
  description = "The current power state of the server (null when unset)."
  value       = try(one(netcup_server_power.example).state, null)
}
