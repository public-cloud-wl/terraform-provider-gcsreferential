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

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), fullPath)

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

	cachedPool, err := getAndCacheIdPool(ctx, r.providerData, data.Name.ValueString(), &gcpConnector)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot find id_pool: %s on %s: %s", data.Name.ValueString(), r.providerData.ReferentialBucket.ValueString(), err.Error()))
		return
	}

	cachedPool.Mutex.Lock()
	defer cachedPool.Mutex.Unlock()

	// Make changes.
	namedChanged := data.Name != newData.Name
	start_from_changed := data.StartFrom != newData.StartFrom
	end_to_changed := data.EndTo != newData.EndTo

	if start_from_changed || end_to_changed {

		// Check members.
		for k, v := range cachedPool.Pool.Members {
			if v < IdPoolTools.ID(newData.StartFrom.ValueInt64()) || v > IdPoolTools.ID(newData.EndTo.ValueInt64()) {
				tflog.Error(ctx, fmt.Sprintf("Failed change pool %s, still a member that cannot fit into new limits: %s, that have value: %d", newData.Name.ValueString(), k, v))
				resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Failed change pool %s, still a member that cannot fit into new limits: %s, that have value: %d", newData.Name.ValueString(), k, v))
				return
			}
		}

		cachedPool.Pool.StartFrom = IdPoolTools.ID(newData.StartFrom.ValueInt64())
		cachedPool.Pool.EndTo = IdPoolTools.ID(newData.EndTo.ValueInt64())

		// start_from change
		if newData.StartFrom.ValueInt64() < data.StartFrom.ValueInt64() {
			// Add new IDs
			loopIndex := newData.StartFrom.ValueInt64()
			for loopIndex < data.StartFrom.ValueInt64() {
				cachedPool.Pool.Insert(IdPoolTools.ID(loopIndex))
				loopIndex++
			}
		} else {
			// Remove no more available IDs.
			loopIndex := data.StartFrom.ValueInt64()
			for loopIndex < newData.StartFrom.ValueInt64() {
				cachedPool.Pool.Remove(IdPoolTools.ID(loopIndex))
				loopIndex++
			}
		}
		// end_to change
		if newData.EndTo.ValueInt64() > data.EndTo.ValueInt64() {
			// Add new IDs
			loopIndex := data.EndTo.ValueInt64() + 1
			for loopIndex <= newData.EndTo.ValueInt64() {
				cachedPool.Pool.Insert(IdPoolTools.ID(loopIndex))
				loopIndex++
			}
		} else {
			// Remove no more available IDs.
			loopIndex := data.EndTo.ValueInt64()
			for loopIndex > newData.EndTo.ValueInt64() {
				cachedPool.Pool.Remove(IdPoolTools.ID(loopIndex))
				loopIndex--
			}
		}
	}

	newConnector := gcpConnector
	if namedChanged {
		newFullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, newData.Name.ValueString())
		newConnector = connector.NewGeneric(r.providerData.ReferentialBucket.ValueString(), newFullPath)
	}

	// Write file.
	err = newConnector.Write(ctx, cachedPool.Pool)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot write new id_pool content for: %s on %s ", data.Name.ValueString(), r.providerData.ReferentialBucket.ValueString()))
		return
	}
	// Update generation in cache
	cachedPool.Generation = newConnector.Generation

	// If name changed, invalidate old cache entry
	if namedChanged {
		// Remove old file.
		err = gcpConnector.Delete(ctx)
		if err != nil {
			// This is not a fatal error, but we should warn the user. The old file is orphaned.
			resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot remove old id_pool %s after duplicate into new one %s on %s", data.Name.ValueString(), newData.Name.ValueString(), r.providerData.ReferentialBucket.ValueString()))
		}
		// Invalidate old cache
		r.providerData.CacheMutex.Lock()
		delete(r.providerData.IdPoolsCache, data.Name.ValueString())
		r.providerData.CacheMutex.Unlock()
	}
	// Save data into Terraform state.
	newData.Reservations = data.Reservations
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
