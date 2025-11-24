package provider

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccNetworkRequestResource(t *testing.T) {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")
	if bucketName == "" {
		t.Skip("GCS_REFERENTIAL_BUCKET environment variable not set, skipping acceptance test")
	}

	baseCidr := "10.20.0.0/16"
	reqId1 := "test-network-req-1"
	reqId2 := "test-network-req-2"
	reqId3 := "test-network-req-3"

	// Sanitize IDs for use as resource names
	sReqId1 := strings.ReplaceAll(reqId1, "-", "_")
	sReqId2 := strings.ReplaceAll(reqId2, "-", "_")
	sReqId3 := strings.ReplaceAll(reqId3, "-", "_")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// 1. Create two initial requests
			{
				Config: testAccNetworkRequestConfig(baseCidr, 24, reqId1, reqId2),
				Check: resource.ComposeAggregateTestCheckFunc(
					// Check first request
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId1), "id", reqId1),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId1), "base_cidr", baseCidr),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId1), "prefix_length", "24"),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId1), "netmask", "10.20.0.0/24"),
					// Check second request
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId2), "id", reqId2),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId2), "base_cidr", baseCidr),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId2), "prefix_length", "24"),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId2), "netmask", "10.20.1.0/24"),
				),
			},
			// 2. Add a third request to test update
			{
				Config: testAccNetworkRequestConfig(baseCidr, 24, reqId1, reqId2, reqId3),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId3), "id", reqId3),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId3), "netmask", "10.20.2.0/24"),
				),
			},
			// 3. Remove the second request to test deletion and freeing of a subnet
			{
				Config: testAccNetworkRequestConfig(baseCidr, 24, reqId1, reqId3),
				Check: resource.ComposeAggregateTestCheckFunc(
					// Check that req1 and req3 still exist with their original netmasks
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId1), "netmask", "10.20.0.0/24"),
					resource.TestCheckResourceAttr(fmt.Sprintf("gcsreferential_network_request.%s", sReqId3), "netmask", "10.20.2.0/24"),
				),
			},
			// 4. Add back the second request; it should reuse the freed slot
			{
				Config: testAccNetworkRequestConfig(baseCidr, 24, reqId1, reqId2, reqId3),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(fmt.Sprintf("gcsreferential_network_request.%s", sReqId2), "netmask"),
				),
			},
			// 5. Test for error when creating a duplicate ID
			{
				Config:      testAccNetworkRequestConfigDuplicate(baseCidr, 24, reqId1),
				ExpectError: regexp.MustCompile("network_request already exist with this id"),
			},
			// 6. Test for error when the requested prefix is larger than the base CIDR
			{
				Config:      testAccNetworkRequestConfig(baseCidr, 15, "impossible-request"),
				ExpectError: regexp.MustCompile("Cannot find any available subnet in 10.20.0.0/16 with prefix 15"),
			},
		},
	})
}

func testAccNetworkRequestConfig(baseCidr string, prefixLength int, reqIds ...string) string {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")
	config := fmt.Sprintf(`
provider "gcsreferential" {
  referential_bucket = "%s"
}
`, bucketName)

	// Create a dependency chain to ensure deterministic creation order
	for i, id := range reqIds {
		sanitizedId := strings.ReplaceAll(id, "-", "_")
		dependsOn := ""
		if i > 0 {
			prevSanitizedId := strings.ReplaceAll(reqIds[i-1], "-", "_")
			dependsOn = fmt.Sprintf("depends_on = [gcsreferential_network_request.%s]", prevSanitizedId)
		}

		config += fmt.Sprintf(`
resource "gcsreferential_network_request" "%s" {
  base_cidr     = "%s"
  prefix_length = %d
  id            = "%s"
  %s
}
`, sanitizedId, baseCidr, prefixLength, id, dependsOn)
	}
	return config
}

func testAccNetworkRequestConfigDuplicate(baseCidr string, prefixLength int, reqId string) string {
	sanitizedId := strings.ReplaceAll(reqId, "-", "_")
	return testAccNetworkRequestConfig(baseCidr, prefixLength, reqId) + fmt.Sprintf(`
resource "gcsreferential_network_request" "%s_dup" {
  base_cidr     = "%s"
  prefix_length = %d
  id            = "%s"
}
`, sanitizedId, baseCidr, prefixLength, reqId)
}
