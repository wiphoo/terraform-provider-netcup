terraform {
  required_providers {
    netcup = {
      source  = "wiphoo/netcup"
      version = "~> 0.0"
    }
  }
}

provider "netcup" {
  password = "placeholder"
  api_key  = "placeholder"
}

resource "netcup_server_snapshot_restore" "example" {
  server_id    = "12345"
  snapshot_id  = "snap-12345"
  description  = "Example restore from snapshot for documentation"
  comment      = "Restored from snapshot"
}

output "restore_id" {
  description = "The ID of the initiated restore operation"
  value       = netcup_server_snapshot_restore.example.id
}
