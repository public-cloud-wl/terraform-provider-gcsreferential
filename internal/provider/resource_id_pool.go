package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	providerData GCSReferentialProviderModel
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
	providerData, ok := req.ProviderData.(GCSReferentialProviderModel)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider data ", "")
	}
	r.providerData = providerData
}

func getPoolConnector(ctx context.Context, data *IdPoolResourceModel, p GCSReferentialProviderModel, idpool *IdPoolTools.IDPool) connector.GcpConnectorGeneric {
	bucketName := p.ReferentialBucket.ValueString()
	fullPath := fmt.Sprintf("%s/%s/%s", ProviderName, idPoolResourceName, data.Name.ValueString())
	gcpConnector := connector.NewGeneric(bucketName, fullPath)
	// Must stay for getting current generation index.
	err := gcpConnector.Read(ctx, idpool)
	if err != nil {
		tflog.Debug(ctx, fmt.Sprintf("Error on reading id_pool %s on bucket %s", fullPath, bucketName))
	}
	tflog.Debug(ctx, fmt.Sprintf("Using getPoolConnector function that make 1 read for id_pool %s on bucket %s", fullPath, bucketName))
	return gcpConnector
}

func readRemoteIdPool(ctx context.Context, data *IdPoolResourceModel, p GCSReferentialProviderModel, idpool *IdPoolTools.IDPool, existingLock ...uuid.UUID) error {
	gcpConnector := getPoolConnector(ctx, data, p, idpool)
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(p.TimeoutInMinutes.ValueInt32()), p.BackoffMultiplier.ValueFloat32(), existingLock...)
	if err != nil {
		return fmt.Errorf("Fail to acquire lock: %w", err)
	}

	shouldUnlock := len(existingLock) == 0 || lockId != existingLock[0]
	if shouldUnlock {
		defer func() {
			unlockErr := gcpConnector.Unlock(ctx, lockId)
			if unlockErr != nil {
				tflog.Error(ctx, fmt.Sprintf("Failed to unlock %s (%s), %s", data.Name, lockId, unlockErr.Error()))
				// As we are in a defer function (at the end) need to chek last error.
				if err == nil {
					// No error.
					err = fmt.Errorf("Failed to unlock %s (%s)", data.Name, lockId)
				} else {
					err = fmt.Errorf("Failed to unlock %s (%s) AND %s", data.Name, lockId, err.Error())
				}
			} else {
				tflog.Debug(ctx, fmt.Sprintf("Success to unlock %s (%s)", data.Name, lockId))
			}
		}()
	}
	err = gcpConnector.Read(ctx, idpool)
	return err
}

func writeRemoteIdPool(ctx context.Context, data *IdPoolResourceModel, p GCSReferentialProviderModel, idpool *IdPoolTools.IDPool, existingLock ...uuid.UUID) error {
	var tmpIdPool IdPoolTools.IDPool
	gcpConnector := getPoolConnector(ctx, data, p, &tmpIdPool)
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(p.TimeoutInMinutes.ValueInt32()), p.BackoffMultiplier.ValueFloat32(), existingLock...)

	if err != nil {
		return fmt.Errorf("Fail to acquire lock: %w", err)
	}

	shouldUnlock := len(existingLock) == 0 || lockId != existingLock[0]
	if shouldUnlock {
		defer func() {
			unlockErr := gcpConnector.Unlock(ctx, lockId)
			if unlockErr != nil {
				tflog.Error(ctx, fmt.Sprintf("Failed to unlock %s (%s)", data.Name, lockId))
				// As we are in a defer function (at the end) need to chek last error.
				if err == nil {
					// No error.
					err = fmt.Errorf("Failed to unlock %s (%s)", data.Name, lockId)
				} else {
					err = fmt.Errorf("Failed to unlock %s (%s) AND %s", data.Name, lockId, err.Error())
				}
			} else {
				tflog.Debug(ctx, fmt.Sprintf("Success to unlock %s (%s)", data.Name, lockId))
			}
		}()
	}

	err = gcpConnector.Write(ctx, idpool)
	return err
}

func deleteRemoteIdPool(ctx context.Context, data *IdPoolResourceModel, p GCSReferentialProviderModel, existingLock ...uuid.UUID) error {
	gcpConnector := getPoolConnector(ctx, data, p, nil)
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(p.TimeoutInMinutes.ValueInt32()), p.BackoffMultiplier.ValueFloat32(), existingLock...)

	if err != nil {
		return fmt.Errorf("Fail to acquire lock: %w", err)
	}

	shouldUnlock := len(existingLock) == 0 || lockId != existingLock[0]
	if shouldUnlock {
		defer func() {
			unlockErr := gcpConnector.Unlock(ctx, lockId)
			if unlockErr != nil {
				tflog.Error(ctx, fmt.Sprintf("Failed to unlock %s (%s)", data.Name, lockId))
				// As we are in a defer function (at the end) need to chek last error.
				if err == nil {
					// No error.
					err = fmt.Errorf("Failed to unlock %s (%s)", data.Name, lockId)
				} else {
					err = fmt.Errorf("Failed to unlock %s (%s) AND %s", data.Name, lockId, err.Error())
				}
			} else {
				tflog.Debug(ctx, fmt.Sprintf("Success to unlock %s (%s)", data.Name, lockId))
			}
		}()
	}

	err = gcpConnector.Delete(ctx)
	return err
}

func (r *IdPoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data IdPoolResourceModel
	var err error
	var pool IdPoolTools.IDPool
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err = readRemoteIdPool(ctx, &data, r.providerData, &pool)
	if err == nil {
		resp.Diagnostics.AddError("id_pool create error", fmt.Sprintf("Error on creation of %s it already exist, verify you did not make any mistake or consider to import", data.Name))
		return
	}
	data.Id = data.Name
	tflog.Debug(ctx, "I WILL TRY TO CREATE THE POOL")
	pool = *IdPoolTools.NewIDPool(IdPoolTools.ID(data.StartFrom.ValueInt64()), IdPoolTools.ID(data.EndTo.ValueInt64()))
	if !pool.IsValid() {
		tflog.Debug(ctx, fmt.Sprintf("INVALID POOL: %s", data.Name))
		resp.Diagnostics.AddError("id_pool create error", "Invalid pool, please check start_from and end_to")
		return
	}
	emptyGoMap := map[string]attr.Value{}
	data.Reservations, _ = types.MapValue(types.Int64Type, emptyGoMap)
	tflog.Debug(ctx, "WRITING POOL ...")
	err = writeRemoteIdPool(ctx, &data, r.providerData, &pool)
	if err != nil {
		resp.Diagnostics.AddError("id_pool create error", "Cannot save id_pool on referential_bucket")
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IdPoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data IdPoolResourceModel
	var err error
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err = innerPoolRead(ctx, &data, r.providerData)
	if err != nil {
		resp.State.RemoveResource(ctx)
	} else {
		// Save data into Terraform state
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
}

func innerPoolRead(ctx context.Context, data *IdPoolResourceModel, p GCSReferentialProviderModel) error {
	var pool IdPoolTools.IDPool
	err := readRemoteIdPool(ctx, data, p, &pool)
	if err != nil {
		return fmt.Errorf("Cannot read the %s from the %s bucket", data.Name, p.ReferentialBucket)
	}
	err = idPoolFromToolToModel(data, &pool, p)
	if err != nil {
		return err
	}
	return nil
}

func (r *IdPoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data IdPoolResourceModel
	var newData IdPoolResourceModel
	var pool IdPoolTools.IDPool
	var nullstruct struct{}

	resp.Diagnostics.Append(req.Plan.Get(ctx, &newData)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	gcpConnector := getPoolConnector(ctx, &data, r.providerData, &pool)
	lockId, err := gcpConnector.WaitForlock(ctx, time.Minute*time.Duration(r.providerData.TimeoutInMinutes.ValueInt32()), r.providerData.BackoffMultiplier.ValueFloat32())

	if err != nil {
		tflog.Error(ctx, fmt.Sprintf("Fail to acquire lock: %s", err.Error()))
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Fail to acquire lock: %s", err.Error()))
	}

	defer func() {
		unlockErr := gcpConnector.Unlock(ctx, lockId)
		if unlockErr != nil {
			tflog.Error(ctx, fmt.Sprintf("Failed to unlock %s (%s)", data.Name, lockId))
			// As we are in a defer function (at the end) need to chek last error.
			if err == nil {
				// No error.
				err = fmt.Errorf("Failed to unlock %s (%s)", data.Name.ValueString(), lockId)
			} else {
				err = fmt.Errorf("Failed to unlock %s (%s) AND %s", data.Name.ValueString(), lockId, err.Error())
			}
		} else {
			tflog.Debug(ctx, fmt.Sprintf("Success to unlock %s (%s)", data.Name, lockId))
		}
	}()

	err = readRemoteIdPool(ctx, &data, r.providerData, &pool, lockId)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot find id_pool: %s on %s ", data.Name.ValueString(), r.providerData.ReferentialBucket.ValueString()))
	}

	// Make changes.
	namedChanged := data.Name != newData.Name
	start_from_changed := data.StartFrom != newData.StartFrom
	end_to_changed := data.EndTo != newData.EndTo

	if start_from_changed || end_to_changed {

		// Check members.
		for k, v := range pool.Members {
			if v < IdPoolTools.ID(newData.StartFrom.ValueInt64()) || v > IdPoolTools.ID(newData.EndTo.ValueInt64()) {
				tflog.Error(ctx, fmt.Sprintf("Failed change pool %s, still a member that cannot fit into new limits: %s, that have value: %d", newData.Name.ValueString(), k, v))
				resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Failed change pool %s, still a member that cannot fit into new limits: %s, that have value: %d", newData.Name.ValueString(), k, v))
				return
			}
		}

		pool.StartFrom = IdPoolTools.ID(newData.StartFrom.ValueInt64())
		pool.EndTo = IdPoolTools.ID(newData.EndTo.ValueInt64())

		// start_from change
		if newData.StartFrom.ValueInt64() < data.StartFrom.ValueInt64() {
			// Add new IDs
			loopIndex := newData.StartFrom.ValueInt64()
			for loopIndex < data.EndTo.ValueInt64() {
				pool.IdCache.Ids[IdPoolTools.ID(loopIndex)] = nullstruct
				loopIndex++
			}
		} else {
			// Remove no more available IDs.
			loopIndex := data.StartFrom.ValueInt64()
			for loopIndex < newData.StartFrom.ValueInt64() {
				delete(pool.IdCache.Ids, IdPoolTools.ID(loopIndex))
				loopIndex++
			}
		}
		// end_to change
		if newData.EndTo.ValueInt64() > data.EndTo.ValueInt64() {
			// Add new IDs
			loopIndex := data.EndTo.ValueInt64() + 1
			for loopIndex <= newData.EndTo.ValueInt64() {
				pool.IdCache.Ids[IdPoolTools.ID(loopIndex)] = nullstruct
				loopIndex++
			}
		} else {
			// Remove no more available IDs.
			loopIndex := data.EndTo.ValueInt64()
			for loopIndex > newData.EndTo.ValueInt64() {
				delete(pool.IdCache.Ids, IdPoolTools.ID(loopIndex))
				loopIndex--
			}
		}
	}

	// Write file.
	err = writeRemoteIdPool(ctx, &newData, r.providerData, &pool, lockId)
	if err != nil {
		resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot write new id_pool content for: %s on %s ", data.Name.ValueString(), r.providerData.ReferentialBucket.ValueString()))
	}

	if namedChanged {
		// Remove old file.
		err = deleteRemoteIdPool(ctx, &data, r.providerData, lockId)
		if err != nil {
			resp.Diagnostics.AddError("id_pool update error", fmt.Sprintf("Cannot remove old id_pool %s after duplicate into new one %s on %s", data.Name.ValueString(), newData.Name.ValueString(), r.providerData.ReferentialBucket.ValueString()))
		}
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
	err := deleteRemoteIdPool(ctx, &data, r.providerData)
	if err != nil {
		resp.Diagnostics.AddError("id_pool delete error", "Cannot delete id_pool")
		return
	}
}

func (r *IdPoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func idPoolFromToolToModel(data *IdPoolResourceModel, pool *IdPoolTools.IDPool, p GCSReferentialProviderModel) error {
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
