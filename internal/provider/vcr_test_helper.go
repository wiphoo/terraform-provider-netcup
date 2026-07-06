package provider

import (
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

func newVCRClient(t *testing.T, cassetteName string) *netcup.Client {
	t.Helper()
	return vcr.NewClient(t, cassetteName)
}
