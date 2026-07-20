# List image flavours available for installation on a specific server.
data "netcup_server_images" "example" {
  server_id = "12345"
}

output "server_images" {
  description = "Image flavours available for installation on the server."
  value       = data.netcup_server_images.example.images
}
