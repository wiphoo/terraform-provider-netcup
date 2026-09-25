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

resource "netcup_server_snapshot" "example" {
  server_id = "12345"
  comment   = "Example snapshot for documentation"
  tags      = ["terraform", "example"]
}

output "snapshot_id" {
  description = "The ID of the created snapshot"
  value       = netcup_server_snapshot.example.id
}
