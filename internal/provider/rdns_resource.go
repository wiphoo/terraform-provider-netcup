package provider

import (
	"context"
	"fmt"
	"net/netip"

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
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					canonicalIPValidator{},
				},
			},
			"hostname": schema.StringAttribute{
				Required:    true,
				Description: "The fully qualified domain name for the reverse DNS (PTR) entry.",
				Validators: []validator.String{
					canonicalHostnameValidator{},
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

	ip := plan.IPAddress.ValueString()
	hostname := plan.Hostname.ValueString()

	canonical, err := netcup.CanonicalizeIP(ip)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IP address", err.Error())
		return
	}

	if canonical != ip {
		resp.Diagnostics.AddError(
			"Non-canonical IP address",
			fmt.Sprintf("IP address %q must be written in canonical form: use %q instead.", ip, canonical),
		)
		return
	}

	if hostname == "" {
		resp.Diagnostics.AddError("Invalid hostname", "Hostname must not be empty.")
		return
	}

	normalizedHostname := netcup.NormalizeRDNSHostname(hostname)
	if hostname != normalizedHostname {
		resp.Diagnostics.AddError(
			"Non-canonical hostname",
			fmt.Sprintf("Hostname must be in canonical form: use %q instead of %q.", normalizedHostname, hostname),
		)
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
		Hostname:  types.StringValue(netcup.NormalizeRDNSHostname(entry.Hostname)),
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

	ip := plan.IPAddress.ValueString()
	hostname := plan.Hostname.ValueString()

	canonical, err := netcup.CanonicalizeIP(ip)
	if err != nil {
		resp.Diagnostics.AddError("Invalid IP address", err.Error())
		return
	}

	if canonical != ip {
		resp.Diagnostics.AddError(
			"Non-canonical IP address",
			fmt.Sprintf("IP address %q must be written in canonical form: use %q instead.", ip, canonical),
		)
		return
	}

	if hostname == "" {
		resp.Diagnostics.AddError("Invalid hostname", "Hostname must not be empty.")
		return
	}

	normalizedHostname := netcup.NormalizeRDNSHostname(hostname)
	if hostname != normalizedHostname {
		resp.Diagnostics.AddError(
			"Non-canonical hostname",
			fmt.Sprintf("Hostname must be in canonical form: use %q instead of %q.", normalizedHostname, hostname),
		)
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

// canonicalIPValidator rejects IPs that are not in canonical (RFC 5952) form.
// The SDK requires canonical IPs for API calls, so enforcing this up front
// prevents silent normalization surprises and inconsistent plan/apply results.
type canonicalIPValidator struct{}

func (v canonicalIPValidator) Description(_ context.Context) string {
	return "Requires the IP address to be in canonical RFC 5952 form."
}

func (v canonicalIPValidator) MarkdownDescription(_ context.Context) string {
	return "Requires the IP address to be in canonical RFC 5952 form (lowercase, compressed IPv6, no IPv4-in-IPv6 mapping)."
}

func (v canonicalIPValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	addr, err := netip.ParseAddr(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid IP address",
			fmt.Sprintf("Cannot parse %q as a valid IP address.", req.ConfigValue.ValueString()),
		)
		return
	}

	addr = addr.Unmap()
	canonical := addr.String()

	if req.ConfigValue.ValueString() != canonical {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Non-canonical IP address",
			fmt.Sprintf("IP address %q must be written in canonical form: use %q instead.", req.ConfigValue.ValueString(), canonical),
		)
	}
}

// canonicalHostnameValidator rejects hostnames with leading/trailing whitespace
// or that are empty after trimming. The SDK strips whitespace internally, so
// enforcing this up front prevents the planned value from differing from what
// gets sent to the API.
type canonicalHostnameValidator struct{}

func (v canonicalHostnameValidator) Description(_ context.Context) string {
	return "Requires the hostname to be non-empty with no leading or trailing whitespace."
}

func (v canonicalHostnameValidator) MarkdownDescription(_ context.Context) string {
	return "Requires the hostname to be non-empty with no leading or trailing whitespace."
}

func (v canonicalHostnameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	input := req.ConfigValue.ValueString()

	if input == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid hostname",
			"Hostname must not be empty.",
		)
		return
	}

	normalized := netcup.NormalizeRDNSHostname(input)
	if input != normalized {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Non-canonical hostname",
			fmt.Sprintf("Hostname must be in canonical form: use %q instead of %q.", normalized, input),
		)
	}
}
