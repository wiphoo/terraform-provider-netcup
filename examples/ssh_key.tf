# Register an SSH public key in the netcup SCP account. The computed id can be
# passed to netcup_server_reinstall.ssh_key_ids.
resource "netcup_ssh_key" "worker" {
  name       = "toffoli-k3s-key"
  public_key = file(pathexpand("~/.ssh/id_ed25519.pub"))
}

# List all SSH keys registered in the account.
data "netcup_ssh_keys" "all" {}

output "ssh_key_id" {
  value = netcup_ssh_key.worker.id
}
