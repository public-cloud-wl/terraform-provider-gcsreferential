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
				"gcsreferential_network_request": networkRequest(),
			},
			ConfigureContextFunc: providerConfigure,
		}
	}
}
func configure(version string, p *schema.Provider) func(context.Context, data *schema.ResourceData) (any, diag.Diagnostics) {
	gcsreferentialBucket := data.Get("referential_bucket").(string)
	var diags diag.Diagnostics
	if gcsreferentialBucket == "" {
		return nil, diag.Errorf("referential_bucket is not set!")
	}

	return gcsreferentialBucket, diags
}
