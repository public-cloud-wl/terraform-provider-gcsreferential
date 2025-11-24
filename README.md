# Terraform Provider gcsreferential (Terraform Plugin SDK)

Terraform Provider making GCS Bucket a referential for various stuff

# Credits

This one was created based on https://github.com/sbehl27-org/terraform-provider-cidr-reservator

# Run tests

Test cannot be run from github for the moment so you need to run them locally

```bash
gcloud auth login --update-adc
source tests.env
make testacc
```

