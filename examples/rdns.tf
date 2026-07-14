# Manage a reverse DNS (PTR) entry for an IP address.
#
# This example is opt-in: it is skipped unless you supply an IP address you own,
# so a bare `terraform plan` does not propose creating a placeholder PTR record.
# Enable it with:
#
#   terraform plan -var 'rdns_ip_address=203.0.113.10' -var 'rdns_hostname=server.example.com'
#
# Import an existing reverse DNS entry (the enabling variable must be set so
# Terraform can resolve the counted resource address):
#   terraform import -var 'rdns_ip_address=203.0.113.10' 'netcup_rdns.server[0]' 203.0.113.10
variable "rdns_ip_address" {
  description = "IP address to manage a PTR record for. Leave null to skip this example."
  type        = string
  default     = null
}

variable "rdns_hostname" {
  description = "Hostname the PTR record should resolve to."
  type        = string
  default     = "server.example.com"
}

resource "netcup_rdns" "server" {
  count      = var.rdns_ip_address == null ? 0 : 1
  ip_address = var.rdns_ip_address
  hostname   = var.rdns_hostname
}

output "rdns_id" {
  description = "The canonical IP address (resource identifier), null when unset."
  value       = try(one(netcup_rdns.server).id, null)
}
