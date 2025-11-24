package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	IdPoolTools "github.com/public-cloud-wl/tools/idPoolTools"
	"github.com/terraform-provider-gcsreferential/internal/provider/connector"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &IdRequestResource{}
var _ resource.ResourceWithImportState = &IdRequestResource{}

const IdRequestResourceName = "id_request"

func NewIdRequestResource() resource.Resource {
	return &IdRequestResource{}
}

type IdRequestResource struct {
	providerData *GCSReferentialProviderModel
}

type IdRequestResourceModel struct {
	Id          types.String `tfsdk:"id"`
	Pool        types.String `tfsdk:"pool"`
	RequestedId types.Int64  `tfsdk:"requested_id"`
}

func (r *IdRequestResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + IdRequestResourceName
}

func (r *IdRequestResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "This resource allow you to request and id from an id_pool",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The terraform id of the resource",
				Optional:            false,
				Required:            true,
			},
			"pool": schema.StringAttribute{
				MarkdownDescription: "The name of the pool, to make the id_request on. If you change it, the id_request will be destroyed and recreate",
				Optional:            false,
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"requested_id": schema.Int64Attribute{
				MarkdownDescription: "The requested id from the pool, a free one that will be reserved for this resource",
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *IdRequestResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *IdRequestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data IdRequestResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Pool.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_request creation error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Pool.ValueString(), err.Error()))
		return
	}
	defer gcpConnector.Unlock(ctx, lockId)

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Pool.ValueString(), &gcpConnector)
	if err != nil {
		resp.Diagnostics.AddError("id_request creation error", fmt.Sprintf("Cannot find pool '%s' to make the id_request on: %s", data.Pool.ValueString(), err.Error()))
		return
	}

	cachedPool.Mutex.Lock()
	defer cachedPool.Mutex.Unlock()

	_, ok := cachedPool.Pool.Members[data.Id.ValueString()]
	if ok {
		resp.Diagnostics.AddError("id_request creation error", "The id of your id_request is already present in the pool, be sure you did not make any mistake, or consider to import")
		return
	}
	generatedId := cachedPool.Pool.AllocateID(data.Id.ValueString())
	if generatedId == IdPoolTools.NoID {
		resp.Diagnostics.AddError("id_request creation error", "There is no more id available in the pool")
		return
	}
	data.RequestedId = types.Int64Value(int64(generatedId))

	err = gcpConnector.Write(ctx, cachedPool.Pool)
	if err != nil {
		resp.Diagnostics.AddError("id_request creation error", fmt.Sprintf("Cannot update pool on the referential_bucket: %s", err.Error()))
		return
	}
	cachedPool.Generation = gcpConnector.Generation

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IdRequestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data IdRequestResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, fmt.Sprintf("Start read id_request %s", data.Id))

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Pool.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Pool.ValueString(), &gcpConnector)
	if err != nil {
		resp.Diagnostics.AddError("id_request read error", fmt.Sprintf("Cannot find pool '%s' to make the id_request on: %s", data.Pool.ValueString(), err.Error()))
		return
	}
	tflog.Debug(ctx, fmt.Sprintf("Get value %s", data.Id))
	value, ok := cachedPool.Pool.Members[data.Id.ValueString()]
	if !ok {
		tflog.Warn(ctx, fmt.Sprintf("id_request %s not found in pool %s, removing from state.", data.Id.ValueString(), data.Pool.ValueString()))
		resp.State.RemoveResource(ctx)
		return
	}
	tflog.Debug(ctx, fmt.Sprintf("SAVE THE ID %s", value))
	data.RequestedId = types.Int64Value(int64(value))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

}

func (r *IdRequestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data IdRequestResourceModel
	var newData IdRequestResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &newData)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Pool.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_request update error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Pool.ValueString(), err.Error()))
		return
	}
	defer gcpConnector.Unlock(ctx, lockId)

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Pool.ValueString(), &gcpConnector)
	if err != nil {
		resp.Diagnostics.AddError("id_request update error", fmt.Sprintf("Cannot get id_pool from id_request.pool on the referential_bucket: %s", err.Error()))
		return
	}

	cachedPool.Mutex.Lock()
	defer cachedPool.Mutex.Unlock()

	value, ok := cachedPool.Pool.Members[data.Id.ValueString()]
	if !ok {
		resp.Diagnostics.AddError("id_request update error", "Cannot find your id_request in the referential_bucket")
		return
	}
	cachedPool.Pool.Members[newData.Id.ValueString()] = value
	delete(cachedPool.Pool.Members, data.Id.ValueString())

	err = gcpConnector.Write(ctx, cachedPool.Pool)
	if err != nil {
		resp.Diagnostics.AddError("id_request update error", fmt.Sprintf("Cannot update pool on the referential_bucket: %s", err.Error()))
		return
	}
	cachedPool.Generation = gcpConnector.Generation

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &newData)...)
}

func (r *IdRequestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data IdRequestResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Pool.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_request delete error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Pool.ValueString(), err.Error()))
		return
	}
	defer gcpConnector.Unlock(ctx, lockId)

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Pool.ValueString(), &gcpConnector)
	if err != nil {
		// If the pool doesn't exist, the request is already gone. Not an error.
		tflog.Warn(ctx, fmt.Sprintf("Pool %s not found during id_request delete. Assuming request is already gone.", data.Pool.ValueString()))
		return
	}

	cachedPool.Mutex.Lock()
	defer cachedPool.Mutex.Unlock()

	value, ok := cachedPool.Pool.Members[data.Id.ValueString()]
	if !ok {
		// If the member is not found, it's already been deleted. This is not an error.
		tflog.Warn(ctx, fmt.Sprintf("id_request %s not found in pool %s during delete. It may have already been removed.", data.Id.ValueString(), data.Pool.ValueString()))
		return
	}
	cachedPool.Pool.Release(value)

	err = gcpConnector.Write(ctx, cachedPool.Pool)
	if err != nil {
		resp.Diagnostics.AddError("id_request delete error", fmt.Sprintf("Cannot update pool on the referential_bucket: %s", err.Error()))
		return
	}
	cachedPool.Generation = gcpConnector.Generation
}

func (r *IdRequestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idParts := strings.Split(req.ID, "/")
	if len(idParts) != 2 || idParts[0] == "" || idParts[1] == "" {
		resp.Diagnostics.AddError(
			"Unexpected Import Identifier",
			fmt.Sprintf("Expected import identifier with format: pool_name/request_id. Got: %q", req.ID),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("pool"), idParts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), idParts[1])...)
}
