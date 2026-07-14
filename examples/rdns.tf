# Manage a reverse DNS (PTR) entry for an IP address.
#
# This example is opt-in: it is skipped unless you supply both an IP address and
# hostname, so a bare `terraform plan` does not propose creating a placeholder
# PTR record. Enable it with:
#
#   terraform plan \
#     -var 'rdns_ip_address=203.0.113.10' \
#     -var 'rdns_hostname=server.example.com'
#
# Import an existing reverse DNS entry (both enabling variables must be set so
# Terraform can resolve the counted resource address).
#
# Look up the current PTR hostname for your IP first and supply it as
# rdns_hostname so the configuration matches the live state — otherwise
# the next plan will propose overwriting the real PTR with a different value:
#
#   netcupctl rdns get 203.0.113.10
#   terraform import \
#     -var 'rdns_ip_address=203.0.113.10' \
#     -var 'rdns_hostname=REAL_HOSTNAME_FROM_LOOKUP' \
#     'netcup_rdns.server[0]' 203.0.113.10
variable "rdns_ip_address" {
  description = "IP address to manage a PTR record for. Leave null to skip this example."
  type        = string
  default     = null
}

variable "rdns_hostname" {
  description = "Hostname the PTR record should resolve to. Leave null to skip this example."
  type        = string
  default     = null
}

resource "netcup_rdns" "server" {
  count      = var.rdns_ip_address != null && var.rdns_hostname != null ? 1 : 0
  ip_address = var.rdns_ip_address
  hostname   = var.rdns_hostname
}

output "rdns_id" {
  description = "The canonical IP address (resource identifier), null when unset."
  value       = try(one(netcup_rdns.server).id, null)
}
