package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccIdRequestResource_LargeScale(t *testing.T) {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")
	if bucketName == "" {
		t.Skip("GCS_REFERENTIAL_BUCKET environment variable not set, skipping acceptance test")
	}

	reqIds10 := generateRequestIds(10)
	reqIds11 := generateRequestIds(11)
	reqIds13 := generateRequestIds(13)
	reqIds5 := generateRequestIds(5)
	var nullList []string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create a pool and 11 requests (10 generated + 1 static)
			{
				Config: testAccIdRequestResourceConfig(1, 11, reqIds10),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "name", "test-pool-for-requests-large"),
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.test_req2", "requested_id"),
				),
			},
			// Check after refresh there is 10 + 1 reservations
			{
				RefreshState: true,
				Check:        resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "reservations.%", "11"),
			},
			// Check pool is full
			{
				Config:      testAccIdRequestResourceConfig(1, 11, reqIds11),
				ExpectError: regexp.MustCompile("There is no more id available in the pool"),
			},
			// Check extend pool and can book 2 new request dynamic
			{
				Config: testAccIdRequestResourceConfig(0, 12, reqIds13),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.req-12", "requested_id"),
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.req-13", "requested_id"),
				),
			},
			// Update: remove to keep 5 + 1 requests + resize down the pool
			{
				Config: testAccIdRequestResourceConfig(0, 12, reqIds5),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.req-5", "requested_id"),
				),
			},
			{
				RefreshState: true,
				Check:        resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "reservations.%", "6"),
			},
			// Remove all requests but keep the static one
			{
				Config: testAccIdRequestResourceConfig(1, 11, nullList),
			},
			{
				RefreshState: true,
				Check:        resource.TestCheckResourceAttr("gcsreferential_id_pool.test", "reservations.%", "1"),
			},
		},
	})
}

func generateRequestIds(count int) []string {
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		ids[i] = fmt.Sprintf("req-%d", i+1)
	}
	return ids
}

func testAccIdRequestResourceConfig(start int, end int, reqIds []string) string {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")

	returned := fmt.Sprintf(`
	provider "gcsreferential" {
	  referential_bucket = "%s"
	}
	
	resource "gcsreferential_id_pool" "test" {
	  name       = "test-pool-for-requests-large"
	  start_from = %d
	  end_to     = %d
	}

	resource "gcsreferential_id_request" "test_req2" {
		pool = gcsreferential_id_pool.test.name
		id   = "test"
	  }
	
	`, bucketName, start, end)

	for _, i := range reqIds {
		tmp_req := fmt.Sprintf(`
		resource "gcsreferential_id_request" "%s" {
			pool = gcsreferential_id_pool.test.name
			id   = "%s"
		  }

		`, i, i)
		returned += tmp_req
	}

	return returned
}
