# List all snapshots for a specific server.
data "netcup_server_snapshots" "example" {
  server_id = "12345"
}

output "snapshots" {
  description = "All snapshots for the server."
  value       = data.netcup_server_snapshots.example.snapshots
}
