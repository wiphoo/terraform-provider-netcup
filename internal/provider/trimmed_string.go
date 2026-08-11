package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// trimmedStringType is a string custom type whose values are semantically equal
// when they differ only by surrounding whitespace. It backs the netcup_ssh_key
// `name` and `public_key` attributes so a whitespace-only change — e.g.
// `file(...)` vs `trimspace(file(...))`, or the trailing newline a key file
// usually carries — is NOT treated as a change. That keeps RequiresReplace from
// firing on a cosmetic edit and minting a new server-assigned id, which (when
// the id feeds netcup_server_reinstall.ssh_key_ids) would cascade into a
// DESTRUCTIVE server reinstall / disk wipe for a semantic no-op.
//
// Semantic equality is the framework-sanctioned mechanism for this. A plan
// modifier that rewrote the (Required, non-computed) config value would instead
// make Terraform reject the result as a "provider produced invalid plan".
type trimmedStringType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = trimmedStringType{}

func (t trimmedStringType) Equal(o attr.Type) bool {
	other, ok := o.(trimmedStringType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t trimmedStringType) String() string {
	return "provider.trimmedStringType"
}

func (t trimmedStringType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return trimmedStringValue{StringValue: in}, nil
}

func (t trimmedStringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T from StringType.ValueFromTerraform", attrValue)
	}
	return trimmedStringValue{StringValue: stringValue}, nil
}

func (t trimmedStringType) ValueType(_ context.Context) attr.Value {
	return trimmedStringValue{}
}

// trimmedStringValue is the value type produced by trimmedStringType.
type trimmedStringValue struct {
	basetypes.StringValue
}

var _ basetypes.StringValuableWithSemanticEquals = trimmedStringValue{}

func (v trimmedStringValue) Type(_ context.Context) attr.Type {
	return trimmedStringType{}
}

func (v trimmedStringValue) Equal(o attr.Value) bool {
	other, ok := o.(trimmedStringValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

// StringSemanticEquals treats two values as equal when they are identical after
// trimming surrounding whitespace, so a whitespace-only change is a no-op (no
// diff, no replacement).
func (v trimmedStringValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(trimmedStringValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("expected value type trimmedStringValue but got %T", newValuable),
		)
		return false, diags
	}
	if v.IsUnknown() != newValue.IsUnknown() || v.IsNull() != newValue.IsNull() {
		return false, diags
	}
	if v.IsUnknown() || v.IsNull() {
		return true, diags
	}
	return strings.TrimSpace(v.ValueString()) == strings.TrimSpace(newValue.ValueString()), diags
}
