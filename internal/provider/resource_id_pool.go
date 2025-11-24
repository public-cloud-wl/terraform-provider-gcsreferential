package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/storage"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	IdPoolTools "github.com/public-cloud-wl/tools/idPoolTools"
	"github.com/terraform-provider-gcsreferential/internal/provider/connector"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &IdPoolResource{}
var _ resource.ResourceWithImportState = &IdPoolResource{}

const idPoolResourceName = "id_pool"

func NewIdPoolResource() resource.Resource {
	return &IdPoolResource{}
}

type IdPoolResource struct {
	providerData *GCSReferentialProviderModel
}

type IdPoolResourceModel struct {
	Id           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	StartFrom    types.Int64  `tfsdk:"start_from"`
	EndTo        types.Int64  `tfsdk:"end_to"`
	Reservations types.Map    `tfsdk:"reservations"`
}

func (r *IdPoolResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + idPoolResourceName
}

func (r *IdPoolResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "This resource allow you to declare a pool with a name that must be unique, you can then use id_request to request an id from this id_pool",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The terraform id of the resource",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the pool, it must be unique for the provider. If you change it, the pool will be destroyed and recreate",
				Optional:            false,
				Required:            true,
			},
			"start_from": schema.Int64Attribute{
				MarkdownDescription: "The first id of the created pool, if you not set it it will be set to 1",
				Optional:            true,
				Default:             int64default.StaticInt64(1),
				Computed:            true,
			},
			"end_to": schema.Int64Attribute{
				MarkdownDescription: "The last id of the created pool, if you not set it it will be set to 9223372036854775807",
				Optional:            true,
				Default:             int64default.StaticInt64(9223372036854775807),
				Computed:            true,
			},
			"reservations": schema.MapAttribute{
				MarkdownDescription: "The existing reservation made on this pool, it is a readonly field",
				ElementType:         types.Int64Type,
				Computed:            true,
			},
		},
	}
}

func (r *IdPoolResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *IdPoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data IdPoolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_pool create error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Name.ValueString(), err.Error()))
		return
	}
	defer func() {
		if err := gcpConnector.Unlock(ctx, lockId); err != nil {
			tflog.Warn(ctx, fmt.Sprintf("Failed to unlock pool %s, manual intervention may be required to remove lock file: %s", data.Name.ValueString(), err.Error()))
		}
	}()

	// Use the caching helper to check for existence.
	_, err = getAndCacheIdPool(ctx, r.providerData, data.Name.ValueString(), &gcpConnector)
	if err == nil {
		resp.Diagnostics.AddError(
			"id_pool create error",
			fmt.Sprintf("Pool '%s' already exists. To manage this existing pool, please import it.", data.Name.ValueString()),
		)
		return
	}
	if !errors.Is(err, storage.ErrObjectNotExist) {
		resp.Diagnostics.AddError("id_pool create error", fmt.Sprintf("Failed to check for existing pool '%s': %s", data.Name.ValueString(), err.Error()))
		return
	}

	pool := *IdPoolTools.NewIDPool(IdPoolTools.ID(data.StartFrom.ValueInt64()), IdPoolTools.ID(data.EndTo.ValueInt64()))
	if !pool.IsValid() {
		resp.Diagnostics.AddError("id_pool create error", "Invalid pool, please check start_from and end_to")
		return
	}

	// The connector's generation is -1 because Read failed. This will cause Write to use DoesNotExist condition.
	err = gcpConnector.Write(ctx, &pool)
	if err != nil {
		resp.Diagnostics.AddError("id_pool create error", fmt.Sprintf("Cannot save id_pool on referential_bucket: %s", err.Error()))
		return
	}

	// After a successful write, the pool is created. We can warm up the cache.
	// The lock is still held, so this is safe.
	if _, err := getAndCacheIdPool(ctx, r.providerData, data.Name.ValueString(), &gcpConnector); err != nil {
		tflog.Warn(ctx, fmt.Sprintf("Failed to warm cache for pool %s after creation: %s", data.Name.ValueString(), err.Error()))
		resp.Diagnostics.AddWarning("id_pool create warning", fmt.Sprintf("Failed to warm cache for pool %s after creation: %s", data.Name.ValueString(), err.Error()))
	}

	data.Id = data.Name
	emptyGoMap := map[string]attr.Value{}
	data.Reservations, _ = types.MapValue(types.Int64Type, emptyGoMap)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IdPoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data IdPoolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Name.ValueString(), &gcpConnector)
	if err != nil {
		tflog.Warn(ctx, fmt.Sprintf("Pool %s not found, removing from state.", data.Name.ValueString()))
		resp.State.RemoveResource(ctx)
		return
	}

	err = idPoolFromToolToModel(&data, cachedPool.Pool, r.providerData)
	if err != nil {
		resp.Diagnostics.AddError("id_pool read error", fmt.Sprintf("Failed to process pool data for %s: %s", data.Name.ValueString(), err.Error()))
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IdPoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data IdPoolResourceModel
	var newData IdPoolResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &newData)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Determine if the pool is being renamed.
	nameChanged := !data.Name.Equal(newData.Name)

	// Set up connector for the *old* pool name to acquire the lock.
	oldFullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), oldFullPath)

	// Acquire lock on the old pool name to prevent concurrent modifications.
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Name.ValueString(), err.Error()))
		return
	}
	defer func() {
		if err := gcpConnector.Unlock(ctx, lockId); err != nil {
			tflog.Warn(ctx, fmt.Sprintf("Failed to unlock pool %s, manual intervention may be required to remove lock file: %s", data.Name.ValueString(), err.Error()))
		}
	}()

	// Since this is an update, we must read the current state directly from GCS, bypassing the cache.
	var currentPool IdPoolTools.IDPool
	err = gcpConnector.Read(ctx, &currentPool)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot update pool '%s' because it was deleted outside of Terraform.", data.Name.ValueString()))
		} else {
			resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot read id_pool '%s' for update: %s", data.Name.ValueString(), err.Error()))
		}
		return
	}

	// Check if any existing members would be outside the new range.
	for k, v := range currentPool.Members {
		if v < IdPoolTools.ID(newData.StartFrom.ValueInt64()) || v > IdPoolTools.ID(newData.EndTo.ValueInt64()) {
			resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Failed change pool %s, still a member that cannot fit into new limits: %s, that have value: %d", newData.Name.ValueString(), k, v))
			return
		}
	}

	// Rebuild the pool from scratch with the new range and existing members. This is the safest way to handle range changes.
	rebuiltPool := IdPoolTools.NewIDPool(IdPoolTools.ID(newData.StartFrom.ValueInt64()), IdPoolTools.ID(newData.EndTo.ValueInt64()))
	for _, allocatedID := range currentPool.Members {
		rebuiltPool.Remove(allocatedID)
	}
	rebuiltPool.Members = currentPool.Members

	// Determine which connector to use for writing.
	writeConnector := gcpConnector
	if nameChanged {
		newFullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, newData.Name.ValueString())
		writeConnector = connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), newFullPath)
		// When renaming, the new file must not exist.
		writeConnector.Generation = -1
	}

	// Write the updated pool state.
	err = writeConnector.Write(ctx, rebuiltPool)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot write updated id_pool '%s': %s", newData.Name.ValueString(), err.Error()))
		return
	}

	// Invalidate the cache for this pool. This is safer than trying to update it
	// in-place and ensures the next operation reads the fresh state from GCS.
	r.providerData.CacheMutex.Lock()
	delete(r.providerData.IdPoolsCache, data.Name.ValueString())
	if nameChanged {
		delete(r.providerData.IdPoolsCache, newData.Name.ValueString())
	}
	r.providerData.CacheMutex.Unlock()

	// If the name changed, delete the old pool file.
	if nameChanged {
		// Remove old file.
		err = gcpConnector.Delete(ctx)
		if err != nil {
			// This is not a fatal error, but we should warn the user. The old file is orphaned.
			resp.Diagnostics.AddWarning("Orphaned pool file", fmt.Sprintf("Successfully renamed pool to '%s', but failed to delete the old file at '%s'. Manual cleanup may be required. Error: %s", newData.Name.ValueString(), oldFullPath, err.Error()))
		}
	}

	// Now, correctly populate the `newData` model to be saved into state.
	// This is the fix for the "refresh plan was not empty" error.
	newData.Id = data.Id // The ID must remain constant through updates.
	err = idPoolFromToolToModel(&newData, rebuiltPool, r.providerData)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Failed to process updated pool data for %s: %s", newData.Name.ValueString(), err.Error()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newData)...)

}

func (r *IdPoolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data IdPoolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())
	if err != nil {
		resp.Diagnostics.AddError("id_pool delete error", fmt.Sprintf("Cannot acquire lock for pool %s: %s", data.Name.ValueString(), err.Error()))
		return
	}
	defer func() {
		if err := gcpConnector.Unlock(ctx, lockId); err != nil {
			tflog.Warn(ctx, fmt.Sprintf("Failed to unlock pool %s, manual intervention may be required to remove lock file: %s", data.Name.ValueString(), err.Error()))
		}
	}()

	err = gcpConnector.Delete(ctx)
	if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		resp.Diagnostics.AddError("id_pool delete error", fmt.Sprintf("Cannot delete id_pool %s: %s", data.Name.ValueString(), err.Error()))
	}

	// Invalidate cache
	r.providerData.CacheMutex.Lock()
	delete(r.providerData.IdPoolsCache, data.Name.ValueString())
	r.providerData.CacheMutex.Unlock()
}

func (r *IdPoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func idPoolFromToolToModel(data *IdPoolResourceModel, pool *IdPoolTools.IDPool, p *GCSReferentialProviderModel) error {
	if !pool.IsValid() {
		return fmt.Errorf("Something append with the %s from the %s bucket that invalidate it", data.Name, p.ReferentialBucket)
	}
	data.StartFrom = types.Int64Value(int64(pool.StartFrom))
	data.EndTo = types.Int64Value(int64(pool.EndTo))
	reservations := make(map[string]attr.Value)
	for k, m := range pool.Members {
		reservations[k] = types.Int64Value(int64(m))
	}
	data.Reservations, _ = types.MapValue(types.Int64Type, reservations)
	return nil
}
