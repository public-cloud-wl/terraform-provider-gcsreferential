package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testAccProtoV6ProviderFactories are used to instantiate a provider during
// acceptance testing. The factory function will be invoked for every Terraform
// CLI command executed to create a provider server to which the CLI can
// reattach.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"gcsreferential": providerserver.NewProtocol6WithError(New("test")()),
}

func TestAccIdPoolResource(t *testing.T) {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")
	if bucketName == "" {
		t.Skip("GCS_REFERENTIAL_BUCKET environment variable not set, skipping acceptance test")
	}

	poolName1 := "test-pool-initial"
	poolName2 := "test_renamed"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// 1. Create
			{
				Config: testAccIdPoolResourceConfig(poolName1, 1, 10),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "name", poolName1),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "start_from", "1"),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "end_to", "10"),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "id", poolName1),
				),
			},
			// 2. Update name and range
			{
				Config: testAccIdPoolResourceConfig(poolName2, 2, 14),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "name", poolName2),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "start_from", "2"),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "end_to", "14"),
				),
			},
			// 3. Update range again (expand)
			{
				Config: testAccIdPoolResourceConfig(poolName2, 1, 20),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "name", poolName2),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "start_from", "1"),
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "end_to", "20"),
				),
			},
			// 4. Check cannot duplicate
			{
				Config:      testAccIdPoolResourceConfig(poolName2, 1, 20) + "\n" + testAccIdPoolResourceConfig_duplicate_pool(poolName2, 1, 20),
				ExpectError: regexp.MustCompile("it already exist"),
			},
		},
	})
}

func testAccIdPoolResourceConfig(poolName string, start int, end int) string {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")
	return fmt.Sprintf(`
provider "gcsreferential" {
  referential_bucket = "%s"
}

resource "gcsreferential_id_pool" "test" {
  name       = "%s"
  start_from = %d
  end_to     = %d
}
`, bucketName, poolName, start, end)
}

func testAccIdPoolResourceConfig_duplicate_pool(poolName string, start int, end int) string {
	returned := fmt.Sprintf(`
	resource "gcsreferential_id_pool" "test2" {
	  name       = "%s"
	  start_from = %d
	  end_to     = %d
	}
	`, poolName, start, end)

	return returned
}
