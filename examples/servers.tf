# List all servers accessible to the authenticated netcup account.
data "netcup_servers" "all" {}

output "servers" {
  description = "All servers accessible to the authenticated account."
  value       = data.netcup_servers.all.servers
}
