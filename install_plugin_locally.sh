#!/bin/bash

providerName=terraform-provider-gcsreferential

rm $providerName || true
pluginDir=/tmp/test/.terraform/providers/registry.terraform.io/public-cloud-wl/gcsreferential/1.0.7/linux_amd64
rm $pluginDir/$providerName* || true
go build
mkdir -p $pluginDir
cp $providerName $pluginDir/
