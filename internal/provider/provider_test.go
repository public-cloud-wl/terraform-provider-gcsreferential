// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"
)

// providerFactories are used to instantiate a provider during acceptance testing.
// The factory function will be invoked for every Terraform CLI command executed.
// to create a provider server to which the CLI can reattach.

func TestProvider(t *testing.T) {
	p := New("test")
	if p == nil {
		t.Fatal("Failed to instantiate provider")
	}
}
