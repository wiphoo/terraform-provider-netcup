package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTrimmedStringSemanticEquals(t *testing.T) {
	ctx := context.Background()

	mk := func(s string, null, unknown bool) trimmedStringValue {
		switch {
		case null:
			return trimmedStringValue{StringValue: types.StringNull()}
		case unknown:
			return trimmedStringValue{StringValue: types.StringUnknown()}
		default:
			return trimmedStringValue{StringValue: types.StringValue(s)}
		}
	}

	cases := []struct {
		name                     string
		a, b                     string
		aNull, bNull, aUnk, bUnk bool
		want                     bool
	}{
		{name: "trailing newline is equal", a: "ssh-ed25519 AAAA\n", b: "ssh-ed25519 AAAA", want: true},
		{name: "surrounding whitespace is equal", a: "  key  ", b: "key", want: true},
		{name: "different content is not equal", a: "key-one", b: "key-two", want: false},
		{name: "both null are equal", aNull: true, bNull: true, want: true},
		{name: "one null is not equal", aNull: true, b: "key", want: false},
		{name: "both unknown are equal", aUnk: true, bUnk: true, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := mk(tc.a, tc.aNull, tc.aUnk).StringSemanticEquals(ctx, mk(tc.b, tc.bNull, tc.bUnk))
			if diags.HasError() {
				t.Fatalf("unexpected diags: %v", diags)
			}
			if got != tc.want {
				t.Fatalf("StringSemanticEquals(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
