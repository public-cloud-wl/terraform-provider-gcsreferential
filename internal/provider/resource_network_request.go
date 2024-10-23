package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/public-cloud-wl/terraform-provider-gcsreferential/internal/provider/connector"
	"github.com/public-cloud-wl/tools/cidrCalculator"
	"github.com/public-cloud-wl/tools/utils"
)

func networkRequest() *schema.Resource {
	return &schema.Resource{
		CreateContext: networkRequestCreate,
		ReadContext:   networkRequestRead,
		UpdateContext: networkRequestUpdate,
		DeleteContext: networkRequestDelete,

		Schema: map[string]*schema.Schema{
			"prefix_length": {
				Type:     schema.TypeInt,
				Required: true,
			},
			"base_cidr": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"netmask_id": {
				Type:     schema.TypeString,
				Required: true,
			},
			"netmask": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
			// StateContext: importState,
		},
	}
}

func importState(ctx context.Context, data *schema.ResourceData, i interface{}) ([]*schema.ResourceData, error) {
	idContent := strings.Split(data.Id(), ":")
	reservatorBucket := idContent[0]
	baseCidr := idContent[1]
	netmaskId := idContent[2]
	gcpConnector := connector.NewNetwork(reservatorBucket, baseCidr)
	networkConfig, err := gcpConnector.ReadRemote(ctx)
	if err != nil {
		return nil, err
	}
	subnet, contains := networkConfig.Subnets[netmaskId]
	if !contains {
		return nil, errors.New(fmt.Sprintf("Netmask with id %s does not exist!", netmaskId))
	}
	prefixLength := strings.Split(subnet, "/")[1]
	data.Set("base_cidr", baseCidr)
	data.Set("netmask_id", netmaskId)
	data.Set("prefix_length", prefixLength)
	return []*schema.ResourceData{data}, nil
}

func readRemote(ctx context.Context, data *schema.ResourceData, m interface{}) (*connector.NetworkConfig, *connector.GcpConnector, error) {
	cidrProviderBucket := m.(string)
	gcpConnector := connector.NewNetwork(cidrProviderBucket, data.Get("base_cidr").(string))
	networkConfig, err := gcpConnector.ReadRemote(ctx)
	if err != nil {
		if err.Error() == "storage: object doesn't exist" {
			err = nil
			networkConfig = &connector.NetworkConfig{Subnets: make(map[string]string)}
		} else {
			return nil, nil, err
		}
	}
	//netmaskId := data.Get("netmask_id").(string)
	//data.SetId("")
	//if netmask, contains := networkConfig.Subnets[netmaskId]; contains {
	//	data.SetId(netmaskId)
	//	err = data.Set("netmask", netmask)
	//	if err != nil {
	//		return nil, nil, err
	//	}
	//}
	return networkConfig, &gcpConnector, nil
}

func networkRequestCreate(ctx context.Context, data *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	err := utils.Retry(innerResourceServerCreate(ctx, data, m))
	if err != nil {
		return diag.FromErr(err)
	}
	return diags
}

func innerResourceServerCreate(ctx context.Context, data *schema.ResourceData, m interface{}) func() error {
	return func() error {
		networkConfig, gcpConnector, err := readRemote(ctx, data, m)
		if err != nil {
			return err
		}
		if data.Get("id") != nil {
			return nil
		}
		netmaskId := data.Get("netmask_id").(string)
		if _, contains := networkConfig.Subnets[netmaskId]; contains {
			return fmt.Errorf("The netmaskId %s already exists, but does not belong to your Terraform state!!!", netmaskId)
		}
		prefixLength := int8(data.Get("prefix_length").(int))
		if err != nil {
			return err
		}
		newCidrCalculator, err := cidrCalculator.New(&networkConfig.Subnets, prefixLength, gcpConnector.BaseCidrRange)
		if err != nil {
			return err
		}
		nextNetmask, err := newCidrCalculator.GetNextNetmask()
		if err != nil {
			return err
		}
		networkConfig.Subnets[netmaskId] = nextNetmask
		//err = gcpConnector.Recursiveutils.retryReadWrite
		err = gcpConnector.WriteRemote(networkConfig, ctx)
		if err != nil {
			return err
		}
		data.SetId(fmt.Sprintf("%s:%s:%s", gcpConnector.BucketName, gcpConnector.BaseCidrRange, netmaskId))
		err = data.Set("netmask", nextNetmask)
		if err != nil {
			return err
		}
		return nil
	}
}

func networkRequestRead(ctx context.Context, data *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	idContent := strings.Split(data.Id(), ":")
	reservatorBucket := idContent[0]
	baseCidr := idContent[1]
	netmaskId := idContent[2]
	gcpConnector := connector.NewNetwork(reservatorBucket, baseCidr)
	networkConfig, err := gcpConnector.ReadRemote(ctx)
	if err != nil {
		return diag.FromErr(err)
	}
	subnet, contains := networkConfig.Subnets[netmaskId]
	if !contains {
		return diag.FromErr(errors.New(fmt.Sprintf("Netmask with id %s does not exist!", netmaskId)))
	}
	prefixLength := strings.Split(subnet, "/")[1]
	data.Set("base_cidr", baseCidr)
	data.Set("netmask_id", netmaskId)
	data.Set("prefix_length", prefixLength)
	if err != nil {
		return diag.FromErr(err)
	}
	if data.Get("id") != nil {
		return nil
	}
	if _, contains := networkConfig.Subnets[netmaskId]; !contains {
		return diag.FromErr(fmt.Errorf("The netmask for the netmaskId %s does not exist!!!", netmaskId))
	}
	data.Set("netmask", networkConfig.Subnets[netmaskId])
	return diags
}

func networkRequestUpdate(ctx context.Context, data *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	err := utils.Retry(innerResourceServerUpdate(ctx, data, m))
	if err != nil {
		return diag.FromErr(err)
	}
	return diags
}

// TODO: if netmask_id already exists, which does not belong to THIS state, throw error.
// TODO: Update of netmask_id should not enforce recreate.
func innerResourceServerUpdate(ctx context.Context, data *schema.ResourceData, m interface{}) func() error {
	return func() error {
		networkConfig, gcpConnector, err := readRemote(ctx, data, m)
		if err != nil {
			return err
		}
		id := data.Id()
		valuesFromId := strings.Split(id, ":")
		netmaskId := data.Get("netmask_id").(string)
		netmaskIdFromId := valuesFromId[2]
		currentSubnet := networkConfig.Subnets[netmaskIdFromId]
		if netmaskIdFromId != netmaskId {
			delete(networkConfig.Subnets, netmaskIdFromId)
			if _, contains := networkConfig.Subnets[netmaskId]; contains {
				return fmt.Errorf("The netmaskId %s already exists, but does not belong to your Terraform state!!!", netmaskId)
			}
			networkConfig.Subnets[netmaskId] = currentSubnet
		}
		currentPrefixLength, err := strconv.ParseInt(strings.Split(currentSubnet, "/")[1], 10, 8)
		if err != nil {
			return err
		}
		prefixLength := int8(data.Get("prefix_length").(int))
		baseCidrRangeFromId := valuesFromId[1]
		if (baseCidrRangeFromId != gcpConnector.BaseCidrRange) || (int8(currentPrefixLength) != prefixLength) {
			newCidrCalculator, err := cidrCalculator.New(&networkConfig.Subnets, int8(prefixLength), gcpConnector.BaseCidrRange)
			if err != nil {
				return err
			}
			nextNetmask, err := newCidrCalculator.GetNextNetmask()
			if err != nil {
				return err
			}
			networkConfig.Subnets[netmaskId] = nextNetmask
			err = data.Set("netmask", nextNetmask)
			if err != nil {
				return err
			}
		}
		err = gcpConnector.WriteRemote(networkConfig, ctx)
		if err != nil {
			return err
		}
		data.SetId(fmt.Sprintf("%s:%s:%s", gcpConnector.BucketName, gcpConnector.BaseCidrRange, netmaskId))
		return nil
	}
}

func networkRequestDelete(ctx context.Context, data *schema.ResourceData, m interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	err := utils.Retry(innerResourceServerDelete(ctx, data, m))
	if err != nil {
		return diag.FromErr(err)
	}
	return diags
}

func innerResourceServerDelete(ctx context.Context, data *schema.ResourceData, m interface{}) func() error {
	return func() error {
		networkConfig, gcpConnector, err := readRemote(ctx, data, m)
		if err != nil {
			return err
		}
		netmaskId := data.Get("netmask_id").(string)
		delete(networkConfig.Subnets, netmaskId)
		err = gcpConnector.WriteRemote(networkConfig, ctx)
		if err != nil {
			return err
		}
		return nil
	}
}
