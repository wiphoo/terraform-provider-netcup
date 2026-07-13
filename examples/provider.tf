# Terraform provider configuration for netcup.
#
# The provider requires pre-issued tokens minted by `netcupctl auth login`.
# See https://github.com/wiphoo/terraform-provider-netcup for setup instructions.
#
# NOTE: Before the provider is published on the Terraform Registry (planned for
# v1.0.0), you need a dev_overrides CLI configuration to point at a locally-built
# binary. Add something like this to ~/.terraformrc:
#
#   provider_installation {
#     dev_overrides {
#       "wiphoo/netcup" = "/path/to/your/clone/bin"
#     }
#     direct {}
#   }
#
# Then build the provider binary:
#
#   cd /path/to/your/clone
#   go build -o bin/ ./cmd/terraform-provider-netcup

terraform {
  required_providers {
    netcup = {
      source = "wiphoo/netcup"
    }
  }
}

provider "netcup" {
  # Pre-issued tokens (minted by `netcupctl auth login`).
  # Set these via variables or the NETCUP_ACCESS_TOKEN / NETCUP_REFRESH_TOKEN
  # environment variables. When using environment variables, omit or set these
  # to null so the provider's env-var fallback takes effect.
  access_token  = var.netcup_access_token
  refresh_token = var.netcup_refresh_token

  # Optional: override the default API or OIDC endpoints.
  # api_endpoint  = var.netcup_api_endpoint
  # oidc_endpoint = var.netcup_oidc_endpoint
}

variable "netcup_access_token" {
  description = "OAuth 2.0 access token minted by `netcupctl auth login`"
  type        = string
  sensitive   = true
  default     = null
}

variable "netcup_refresh_token" {
  description = "OAuth 2.0 refresh token used to renew the access token"
  type        = string
  sensitive   = true
  default     = null
}

variable "netcup_api_endpoint" {
  description = "Base URL of the SCP REST API"
  type        = string
  default     = "https://www.servercontrolpanel.de/scp-core/api"
}

variable "netcup_oidc_endpoint" {
  description = "Base URL of the SCP OIDC (Keycloak) endpoint"
  type        = string
  default     = "https://www.servercontrolpanel.de/realms/scp/protocol/openid-connect"
}
