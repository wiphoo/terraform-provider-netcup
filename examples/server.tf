# Retrieve details for a single server by its numeric ID.
#
# This example is opt-in: it is skipped unless you supply a real server ID, so
# a bare `terraform plan` does not fail on an account that lacks a placeholder
# server. Enable it with:
#
#   terraform plan -var 'server_id=123456'
variable "server_id" {
  description = "Numeric ID of a server to look up. Leave null to skip this example."
  type        = string
  default     = null
}

data "netcup_server" "x" {
  count = var.server_id == null ? 0 : 1
  id    = var.server_id
}

output "server" {
  description = "Details for the requested server (null when server_id is unset)."
  value       = one(data.netcup_server.x)
}

output "server_ipv4" {
  description = "IPv4 addresses assigned to the server."
  value       = try(one(data.netcup_server.x).ipv4_addresses, null)
}

output "server_ipv6" {
  description = "IPv6 networks assigned to the server."
  value       = try(one(data.netcup_server.x).ipv6_addresses, null)
}
