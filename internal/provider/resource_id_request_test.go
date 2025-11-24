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
	reqIds12 := generateRequestIds(12)
	reqIds5 := generateRequestIds(5)
	var nullList []string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create a pool and 11 requests (10 generated + 1 static)
			{
				Config: testAccIdRequestResourceConfig(2, 12, reqIds10),
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
				Config:      testAccIdRequestResourceConfig(2, 12, reqIds11),
				ExpectError: regexp.MustCompile("There is no more id available in the pool"),
			},
			// Check extend pool and can book 2 new request dynamic
			{
				Config: testAccIdRequestResourceConfig(1, 13, reqIds12),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.req-11", "requested_id"),
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.req-12", "requested_id"),
				),
			},
			// Update: remove to keep 5 + 1 requests + resize down the pool
			{
				Config: testAccIdRequestResourceConfig(1, 13, reqIds5),
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
			{
				Config: testAccIdRequestResourceConfig2(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.test_req2", "requested_id"),
					resource.TestCheckResourceAttrSet("gcsreferential_id_request.test2_req3", "requested_id"),
				),
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

	resource "gcsreferential_id_pool" "test2" {
		name       = "test-pool-for-requests-large-2"
		start_from = 1
		end_to     = 3
	  }

	  resource "gcsreferential_id_request" "test2_req1" {
		pool = gcsreferential_id_pool.test2.name
		id   = "req-1"
	  }

	  resource "gcsreferential_id_request" "test2_req2" {
		pool = gcsreferential_id_pool.test2.name
		id   = "req-2"
	  }

	  resource "gcsreferential_id_request" "test2_req3" {
		pool = gcsreferential_id_pool.test2.name
		id   = "req-3"
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

func testAccIdRequestResourceConfig2() string {
	bucketName := os.Getenv("GCS_REFERENTIAL_BUCKET")

	returned := fmt.Sprintf(`
	provider "gcsreferential" {
	  referential_bucket = "%s"
	}
	provider "gcsreferential" {
		referential_bucket = "%s"
		alias = "test2"
	  }
	
	resource "gcsreferential_id_pool" "test" {
	  name       = "test-pool-multi-provider"
	  start_from = 1
	  end_to     = 100
	}

	  resource "gcsreferential_id_request" "test_req1" {
		pool = gcsreferential_id_pool.test.name
		id   = "req-1"
	  }
	  
	  resource "gcsreferential_id_request" "test_req2" {
		pool = gcsreferential_id_pool.test.name
		id   = "req-2"
	  }

	  resource "gcsreferential_id_request" "test_req3" {
		pool = gcsreferential_id_pool.test.name
		id   = "req-3"
	  }

	  resource "gcsreferential_id_pool" "test2" {
		name       = "test-pool-multi-provider2"
		start_from = 1
		end_to     = 100
		provider = gcsreferential.test2
	  }
	  
  
		resource "gcsreferential_id_request" "test2_req1" {
		  pool = gcsreferential_id_pool.test2.name
		  id   = "req-1"
		  provider = gcsreferential.test2
		}
		
		resource "gcsreferential_id_request" "test2_req2" {
		  pool = gcsreferential_id_pool.test2.name
		  id   = "req-2"
		  provider = gcsreferential.test2
		}
  
		resource "gcsreferential_id_request" "test2_req3" {
		  pool = gcsreferential_id_pool.test2.name
		  id   = "req-3"
		  provider = gcsreferential.test2
		}


		resource "gcsreferential_id_request" "test_cross_req1" {
			pool = gcsreferential_id_pool.test.name
			id   = "req-11"
			provider = gcsreferential.test2
		  }
		  
		  resource "gcsreferential_id_request" "test_cross_req2" {
			pool = gcsreferential_id_pool.test.name
			id   = "req-12"
			provider = gcsreferential.test2
		  }
	
		  resource "gcsreferential_id_request" "test_cross_req3" {
			pool = gcsreferential_id_pool.test.name
			id   = "req-13"
			provider = gcsreferential.test2
		  }

	  `, bucketName, bucketName)

	return returned
}
