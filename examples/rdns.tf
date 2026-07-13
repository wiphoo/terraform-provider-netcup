# Manage a reverse DNS (PTR) entry for an IP address.
#
# Import an existing reverse DNS entry:
#   terraform import netcup_rdns.server 203.0.113.10
resource "netcup_rdns" "server" {
  ip_address = "203.0.113.10"
  hostname   = "server.example.com"
}

output "rdns_id" {
  description = "The canonical IP address (resource identifier)."
  value       = netcup_rdns.server.id
}
