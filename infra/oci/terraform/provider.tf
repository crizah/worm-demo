terraform {
  required_version = ">= 1.5.0"

  required_providers {
    oci = {
      source  = "oracle/oci"
      version = ">= 5.0.0"
    }
  }
}

# reads tenancy/user/fingerprint/key_file/region from ~/.oci/config
# (DEFAULT profile) automatically - nothing to fill in here
provider "oci" {}
