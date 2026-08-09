# Reinstall a server with a native netcup OS image and an optional bootstrap script.
#
# WARNING: Applying or replacing this resource WIPES THE SERVER. All data is
# permanently lost and the server is unavailable while the OS is installed.
# Destroying the resource is safe: it only removes Terraform state and does not
# reinstall or wipe the server.
#
# This example is opt-in. Set reinstall_enabled=true and provide a real server
# ID plus an exact image flavour ID returned by netcup_server_images before applying:
#
#   terraform plan -var 'server_id=123456' \
#     -var 'reinstall_enabled=true' \
#     -var 'reinstall_image_flavour_id=123'

variable "reinstall_enabled" {
  description = "Set to true to enable the destructive reinstall example."
  type        = bool
  default     = false
}

variable "reinstall_image_flavour_id" {
  description = "Exact image flavour ID returned by netcup_server_images."
  type        = number
  default     = null
}

data "netcup_server_images" "reinstall" {
  count     = var.server_id != null && var.reinstall_enabled ? 1 : 0
  server_id = var.server_id
}

locals {
  available_reinstall_images = coalesce(one(data.netcup_server_images.reinstall[*].images), [])
  selected_reinstall_images = [
    for image in local.available_reinstall_images : image
    if var.reinstall_image_flavour_id != null && image.id == var.reinstall_image_flavour_id
  ]
  selected_reinstall_image = one(local.selected_reinstall_images)
}

resource "netcup_server_reinstall" "example" {
  count            = local.selected_reinstall_image == null ? 0 : 1
  server_id        = var.server_id
  image_flavour_id = local.selected_reinstall_image.id

  custom_script = <<-SCRIPT
    #!/bin/sh
    set -eu
    echo "bootstrap completed" >/var/log/netcup-bootstrap.log
  SCRIPT

  # Keep the example synchronous by default. Set wait=false when callers only
  # need the API acceptance and will monitor the task separately.
  wait = true
}

output "selected_reinstall_image" {
  description = "The image flavour selected for the reinstall, or null when disabled."
  value       = local.selected_reinstall_image
}

output "reinstall_task_id" {
  description = "The accepted reinstall task UUID, or null when disabled."
  value       = try(one(netcup_server_reinstall.example).task_id, null)
}
