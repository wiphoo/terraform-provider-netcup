package provider

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

var _ resource.Resource = &rdnsResource{}

var _ resource.ResourceWithConfigure = &rdnsResource{}

var _ resource.ResourceWithImportState = &rdnsResource{}

type rdnsResource struct {
	client *netcup.Client
}

type rdnsResourceModel struct {
	IPAddress types.String `tfsdk:"ip_address"`
	Hostname  types.String `tfsdk:"hostname"`
	ID        types.String `tfsdk:"id"`
}

func NewRDNSResource() resource.Resource {
	return &rdnsResource{}
}

func (r *rdnsResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "netcup_rdns"
}

func (r *rdnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a reverse DNS (PTR) entry for an IP address.",
		Attributes: map[string]schema.Attribute{
			"ip_address": schema.StringAttribute{
				Required:    true,
				Description: "The IP address for the reverse DNS entry. Forces replacement if changed.",
				PlanModifiers: []planmodifier.String{
					canonicalizeIPModifier{},
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					validIPValidator{},
				},
			},
			"hostname": schema.StringAttribute{
				Required:    true,
				Description: "The fully qualified domain name for the reverse DNS (PTR) entry.",
				PlanModifiers: []planmodifier.String{
					trimHostnameModifier{},
				},
				Validators: []validator.String{
					nonEmptyHostnameValidator{},
				},
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The canonical IP address (resource identifier).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *rdnsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*netcup.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *netcup.Client, got %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *rdnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured. Please configure the netcup provider before using netcup_rdns.",
		)
		return
	}

	var plan rdnsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ip := strings.TrimSpace(plan.IPAddress.ValueString())
	hostname := strings.TrimSpace(plan.Hostname.ValueString())

	canonical, err := r.canonicalizeIP(ip)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IP address", err.Error())
		return
	}

	if hostname == "" {
		resp.Diagnostics.AddError("Invalid hostname", "Hostname must not be empty.")
		return
	}

	_, err = r.client.SetRDNS(ctx, canonical, hostname)
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	state := rdnsResourceModel{
		IPAddress: types.StringValue(canonical),
		Hostname:  types.StringValue(hostname),
		ID:        types.StringValue(canonical),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err = r.client.ConfirmRDNS(ctx, canonical, &netcup.RdnsEntry{IP: canonical, Hostname: hostname})
	if err != nil {
		resp.Diagnostics.Append(diag.NewWarningDiagnostic(
			"rDNS confirmation failed",
			fmt.Sprintf("The rDNS entry was set but read-back confirmation did not match within the retry window. The resource is still tracked in state. Error: %v", err),
		))
		return
	}
}

func (r *rdnsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	entry, err := r.client.GetRDNS(ctx, state.ID.ValueString())
	if err != nil {
		diag, gone := apiErrorToDiag(err, false)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diag)
		return
	}

	if entry.Hostname == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	state = rdnsResourceModel{
		IPAddress: types.StringValue(entry.IP),
		Hostname:  types.StringValue(normalizeRDNSHostname(entry.Hostname)),
		ID:        types.StringValue(entry.IP),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rdnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var plan rdnsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ip := strings.TrimSpace(plan.IPAddress.ValueString())
	hostname := strings.TrimSpace(plan.Hostname.ValueString())

	canonical, err := r.canonicalizeIP(ip)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IP address", err.Error())
		return
	}

	if hostname == "" {
		resp.Diagnostics.AddError("Invalid hostname", "Hostname must not be empty.")
		return
	}

	_, err = r.client.SetRDNS(ctx, canonical, hostname)
	if err != nil {
		diag, _ := apiErrorToDiag(err, true)
		resp.Diagnostics.Append(diag)
		return
	}

	state := rdnsResourceModel{
		IPAddress: types.StringValue(canonical),
		Hostname:  types.StringValue(hostname),
		ID:        types.StringValue(canonical),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err = r.client.ConfirmRDNS(ctx, canonical, &netcup.RdnsEntry{IP: canonical, Hostname: hostname})
	if err != nil {
		resp.Diagnostics.Append(diag.NewWarningDiagnostic(
			"rDNS confirmation failed",
			fmt.Sprintf("The rDNS entry was set but read-back confirmation did not match within the retry window. The resource is still tracked in state. Error: %v", err),
		))
		return
	}
}

func (r *rdnsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError(
			"Unconfigured provider",
			"The provider has not been configured.",
		)
		return
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteRDNS(ctx, state.ID.ValueString())
	if err != nil {
		diag, gone := apiErrorToDiag(err, false)
		if gone {
			return
		}
		resp.Diagnostics.Append(diag)
		return
	}
}

func (r *rdnsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *rdnsResource) canonicalizeIP(ip string) (string, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", fmt.Errorf("invalid IP address %q: %w", ip, err)
	}
	if addr.Zone() != "" {
		return "", fmt.Errorf("invalid IP address %q: zone identifiers are not supported", ip)
	}
	addr = addr.Unmap()
	return addr.String(), nil
}

type canonicalizeIPModifier struct{}

func (m canonicalizeIPModifier) Description(_ context.Context) string {
	return "Canonicalizes the IP address to RFC 5952 form."
}

func (m canonicalizeIPModifier) MarkdownDescription(_ context.Context) string {
	return "Canonicalizes the IP address to RFC 5952 form (IPv6 compression, IPv4-in-IPv6 unmapping)."
}

func (m canonicalizeIPModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	addr, err := netip.ParseAddr(req.ConfigValue.ValueString())
	if err != nil {
		return
	}
	addr = addr.Unmap()
	resp.PlanValue = types.StringValue(addr.String())
}

type validIPValidator struct{}

func (v validIPValidator) Description(_ context.Context) string {
	return "Validates that the value is a valid IP address."
}

func (v validIPValidator) MarkdownDescription(_ context.Context) string {
	return "Validates that the value is a valid IPv4 or IPv6 address."
}

func (v validIPValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := netip.ParseAddr(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid IP address",
			fmt.Sprintf("Cannot parse %q as a valid IP address.", req.ConfigValue.ValueString()),
		)
	}
}

type nonEmptyHostnameValidator struct{}

func (v nonEmptyHostnameValidator) Description(_ context.Context) string {
	return "Validates that the hostname is non-empty after trimming whitespace."
}

func (v nonEmptyHostnameValidator) MarkdownDescription(_ context.Context) string {
	return "Validates that the hostname is non-empty after trimming whitespace."
}

func (v nonEmptyHostnameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if strings.TrimSpace(req.ConfigValue.ValueString()) == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid hostname",
			"Hostname must not be empty.",
		)
	}
}

type trimHostnameModifier struct{}

func (m trimHostnameModifier) Description(_ context.Context) string {
	return "Normalizes the hostname: trims whitespace, lowercases, and strips trailing dot."
}

func (m trimHostnameModifier) MarkdownDescription(_ context.Context) string {
	return "Normalizes the hostname (trim whitespace, lowercase, strip trailing dot) at plan time so the planned value and the normalized read-back from refresh always agree."
}

func (m trimHostnameModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	resp.PlanValue = types.StringValue(normalizeRDNSHostname(req.ConfigValue.ValueString()))
}

// normalizeRDNSHostname lowers and strips the trailing dot from a PTR
// read-back, matching the SDK's unexported normalizeRDNSHostname
// (pkg/netcup/rdns.go:245-247). The SDK's GetRDNS already trims whitespace
// before returning the hostname, so only case and trailing-dot differ.
func normalizeRDNSHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(h, "."))
}
