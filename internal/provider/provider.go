package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func New(version string) func() *schema.Provider {
	return func() *schema.Provider {
		return &schema.Provider{
			Schema: map[string]*schema.Schema{
				"referential_bucket": {
					Type:     schema.TypeString,
					Required: true,
				},
			},
			ResourcesMap: map[string]*schema.Resource{
				"gcsreferantial_network_request": networkRequest(),
			},
			ConfigureContextFunc: providerConfigure,
		}
	}
}

func providerConfigure(ctx context.Context, data *schema.ResourceData) (interface{}, diag.Diagnostics) {
	cidrReservatorBucket := data.Get("referential_bucket").(string)
	var diags diag.Diagnostics
	if cidrReservatorBucket == "" {
		return nil, diag.Errorf("referential_bucket is not set!")
	}

	return cidrReservatorBucket, diags
}