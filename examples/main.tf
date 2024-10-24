resource "gcsreferential_network_request" "network_request" {
  prefix_length = 26
  base_cidr     = "10.5.0.0/16"
  netmask_id    = "test"
}

resource "gcsreferential_network_request" "network_request3" {
  prefix_length = 26
  base_cidr     = "10.5.0.0/16"
  netmask_id    = "test2"
}

resource "gcsreferential_network_request" "network_request2" {
  prefix_length = 26
  base_cidr     = "10.6.0.0/18"
  netmask_id    = "test"
}