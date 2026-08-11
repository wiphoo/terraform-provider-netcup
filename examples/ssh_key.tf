# Opt-in SSH-key example. The resource is created only when you pass a public
# key, so a bare `terraform plan` in this directory stays a read-only smoke test:
# it neither requires ~/.ssh/id_ed25519.pub to exist nor plans a real SSH key.
#
#   terraform plan -var 'ssh_public_key=ssh-ed25519 AAAA... you@host'
#
# The computed id can be passed to netcup_server_reinstall.ssh_key_ids.
variable "ssh_public_key" {
  type        = string
  default     = ""
  description = "SSH public key content to register. Empty (default) skips the resource."
}

resource "netcup_ssh_key" "worker" {
  count      = var.ssh_public_key == "" ? 0 : 1
  name       = "toffoli-k3s-key"
  public_key = var.ssh_public_key
}

# Listing registered SSH keys is read-only.
data "netcup_ssh_keys" "all" {}

output "ssh_key_id" {
  value = one(netcup_ssh_key.worker[*].id)
}
