# Retrieve details for a single server by its numeric ID.
data "netcup_server" "x" {
  id = "123456"
}

output "server" {
  description = "Details for the requested server."
  value       = data.netcup_server.x
}

output "server_ipv4" {
  description = "IPv4 addresses assigned to the server."
  value       = data.netcup_server.x.ipv4_addresses
}

output "server_ipv6" {
  description = "IPv6 networks assigned to the server."
  value       = data.netcup_server.x.ipv6_addresses
}
