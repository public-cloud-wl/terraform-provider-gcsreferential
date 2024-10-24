package provider

import(
	"context"
	"fmt"
	
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	
	"github.com/public-cloud-wl/tools/idPoolTools"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &IdPoolResource{}
var _ resource.ResourceWithImportState = &IdPoolResource{}

func NewIdPoolResource() resource.Resource {
	return &IdPoolResource{}
}

type IdPoolResource struct {}

type IdPoolResourceModel struct {
	Id types.string `tfsdk:"id"`
	Name types.string `tfsdk:"name"`
	StartFrom types.int64 `tfsdk:"start_from"`
	EndTo types.int64 `tfsdk:"end_to"`
}

func (r *IdPoolResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_id_pool"
}

func (r *IdPoolResource) Schema()ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema {
		MarkdownDescription: "This resource allow you to declare a pool with a name that must be unique, you can then use id_request to request an id from this id_pool"

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
				Required:			 true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace()
				}
            },
			"start_from": schema.Int64Attribute{
				MarkdownDescription: "The first id of the created pool",
				Optional:            true,
				Default:             int64default.StaticInt64(1)
			}
			"end_to": schema.Int64Attribute{
				MarkdownDescription: "The last id of the created pool",
				Optional:            true,
				Default:             int64default.StaticInt64(9223372036854775807)
			}
		}
	}
}

func (r *IdPoolResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}
}

func readRemote (ctx context.Context, data IdPoolResourceModel, req resource.CreateRequest) (*connector.GcpConnector, error){
	bucketName := req.ProviderData.referential_bucket.ValueString()
	fullPath := fmt.printf("gcsreferential-idpool-%s", data.Name)
	gcpConnector := connector.NewGeneric(bucketName, fullPath)
}

func (r *IdPoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data IdPoolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

    if resp.Diagnostics.HasError() {
        return
    }
	err := utils.Retry(innerCreate(ctx, data))
	if err != nil {
		return resp.Diagnostics.FromErr(err)
	}
	
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func innerCreate(ctx context.Context, data *IdPoolResourceModel) func() error {
	return func() error {
		
	}
}



func (r *IdPoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

    if resp.Diagnostics.HasError() {
        return
    }
}

func (r *IdPoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

    if resp.Diagnostics.HasError() {
        return
    }
}

func (r *IdPoolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

    if resp.Diagnostics.HasError() {
        return
    }
}

func (r *IdPoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}