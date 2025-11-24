package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"cloud.google.com/go/storage"
	cidrCalculator "github.com/public-cloud-wl/tools/cidrCalculator"
	"github.com/terraform-provider-gcsreferential/internal/provider/connector"
)

type networkRequestResource struct {
	providerData *GCSReferentialProviderModel
}

// Metadata implements resource.Resource.
func (r *networkRequestResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_request"
}

type networkRequestResourceModel struct {
	PrefixLength types.Int64  `tfsdk:"prefix_length"`
	BaseCidr     types.String `tfsdk:"base_cidr"`
	Netmask      types.String `tfsdk:"netmask"`
	Id           types.String `tfsdk:"id"`
}

type NetworkConfig struct {
	Subnets map[string]string `json:"subnets"`
}

func NewNetworkRequestResource() resource.Resource {
	return &networkRequestResource{}
}

func (r *networkRequestResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "network_request",
		Attributes: map[string]schema.Attribute{
			"prefix_length": schema.Int64Attribute{
				MarkdownDescription: "The prefix of the requested network for example with 24 a /24 subnet will be booked by the network_request",
				Required:            true,
			},
			"base_cidr": schema.StringAttribute{
				MarkdownDescription: "The supernet where to do the network_request, for example 10.0.0.0/8",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"netmask": schema.StringAttribute{
				MarkdownDescription: "The reserved netmask as full cidr, for example 10.12.13.0/24",
				Computed:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "The id associate to your network_request",
				Required:            true,
			},
		},
	}
}

func (r *networkRequestResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}
	providerData, ok := req.ProviderData.(*GCSReferentialProviderModel)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *GCSReferentialProviderModel, got: %T. Please report this issue to the provider developers.", req.ProviderData))
		return
	}
	r.providerData = providerData
}

func (r *networkRequestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data networkRequestResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gcpConnector := connector.NewNetwork(r.providerData.ReferentialBucket.ValueString(), data.BaseCidr.ValueString())
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("network_request creation error", fmt.Sprintf("Cannot acquire lock for base_cidr %s: %s", data.BaseCidr.ValueString(), err.Error()))
		return
	}
	defer gcpConnector.Unlock(ctx, lockId)

	var networkConfig NetworkConfig
	err = gcpConnector.Read(ctx, &networkConfig)
	if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		resp.Diagnostics.AddError("network_request creation error", fmt.Sprintf("Failed to read network config for %s: %s", data.BaseCidr.ValueString(), err.Error()))
		return
	}

	if networkConfig.Subnets == nil {
		networkConfig.Subnets = make(map[string]string)
	}

	if _, contains := networkConfig.Subnets[data.Id.ValueString()]; contains {
		resp.Diagnostics.AddError("network_request already exist with this id : %s, check your config or consider to import", data.Id.ValueString())
		return
	}

	cidrCalc, err := cidrCalculator.New(&networkConfig.Subnets, int8(data.PrefixLength.ValueInt64()), gcpConnector.BaseCidrRange)
	if err != nil {
		resp.Diagnostics.AddError("network_request creation error", fmt.Sprintf("Fail to get the subnet calculator for the network_request: %s", err.Error()))
		return
	}
	netmask, err := cidrCalc.GetNextNetmask()
	if err != nil {
		resp.Diagnostics.AddError("network_request creation error", fmt.Sprintf("Cannot find any available subnet in %s with prefix %d: %s", gcpConnector.BaseCidrRange, data.PrefixLength.ValueInt64(), err.Error()))
		return
	}
	networkConfig.Subnets[data.Id.ValueString()] = netmask
	err = gcpConnector.Write(ctx, &networkConfig)
	if err != nil {
		resp.Diagnostics.AddError("network_request creation error", fmt.Sprintf("Cannot write network config for %s in %s: %s", gcpConnector.BaseCidrRange, r.providerData.ReferentialBucket.ValueString(), err.Error()))
		return
	}
	data.Netmask = types.StringValue(netmask)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *networkRequestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data networkRequestResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gcpConnector := connector.NewNetwork(r.providerData.ReferentialBucket.ValueString(), data.BaseCidr.ValueString())
	var networkConfig NetworkConfig
	err := gcpConnector.Read(ctx, &networkConfig)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			tflog.Warn(ctx, fmt.Sprintf("Network config for %s not found, removing resource from state", data.BaseCidr.ValueString()))
			resp.State.RemoveResource(ctx)
		} else {
			resp.Diagnostics.AddError("network_request read error", fmt.Sprintf("Cannot Read %s in %s: %s", gcpConnector.BaseCidrRange, r.providerData.ReferentialBucket.ValueString(), err.Error()))
		}
		return
	}

	reservedSubnet, contains := networkConfig.Subnets[data.Id.ValueString()]
	if !contains {
		tflog.Warn(ctx, fmt.Sprintf("Network request %s not found in %s, removing resource from state", data.Id.ValueString(), data.BaseCidr.ValueString()))
		resp.State.RemoveResource(ctx)
		return
	}
	data.Netmask = types.StringValue(reservedSubnet)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *networkRequestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data networkRequestResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *networkRequestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data networkRequestResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gcpConnector := connector.NewNetwork(r.providerData.ReferentialBucket.ValueString(), data.BaseCidr.ValueString())
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("network_request delete error", fmt.Sprintf("Cannot acquire lock for base_cidr %s: %s", data.BaseCidr.ValueString(), err.Error()))
		return
	}
	defer gcpConnector.Unlock(ctx, lockId)

	var networkConfig NetworkConfig
	err = gcpConnector.Read(ctx, &networkConfig)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			// File doesn't exist, so the reservation is already gone.
			return
		}
		resp.Diagnostics.AddError("network_request delete error", fmt.Sprintf("Cannot Read %s in %s: %s", gcpConnector.BaseCidrRange, r.providerData.ReferentialBucket.ValueString(), err.Error()))
		return
	}

	_, contains := networkConfig.Subnets[data.Id.ValueString()]
	if !contains {
		// Reservation doesn't exist, nothing to do.
		return
	}
	delete(networkConfig.Subnets, data.Id.ValueString())
	err = gcpConnector.Write(ctx, &networkConfig)
	if err != nil {
		resp.Diagnostics.AddError("network_request delete error", fmt.Sprintf("Cannot Write %s in %s: %s", gcpConnector.BaseCidrRange, r.providerData.ReferentialBucket.ValueString(), err.Error()))
		return
	}
}

func (r *networkRequestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
