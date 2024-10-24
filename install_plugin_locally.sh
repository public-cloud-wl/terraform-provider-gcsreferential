#!/bin/bash

rm terraform-provider-gcs-referential || true
rm ~/.terraform.d/plugins/terraform-example.com/test/gcs-referential/0.0.1/darwin_arm64/terraform-provider-gcs-referential || true
go build
pluginDir=~/.terraform.d/plugins/terraform-example.com/test/gcs-referential/0.0.1/darwin_arm64/
mkdir -p $pluginDir
cp terraform-provider-gcs-referential $pluginDir/