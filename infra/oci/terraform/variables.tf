variable "compartment_id" {
  description = "compartment OCID to provision into - tenancy OCID (root compartment) is fine for a personal free-tier tenancy"
  type        = string
}

variable "ssh_public_key_path" {
  description = "path to the ssh public key to install on the instance (only the public half - never the private key)"
  type        = string
  default     = "~/Downloads/ssh-key-2026-09-30.key.pub"
}

variable "shape" {
  type    = string
  default = "VM.Standard.A1.Flex"
}

variable "ocpus" {
  type    = number
  default = 1
}

variable "memory_gb" {
  type    = number
  default = 6
}

variable "vcn_cidr" {
  type    = string
  default = "10.0.0.0/16"
}

variable "subnet_cidr" {
  type    = string
  default = "10.0.1.0/24"
}

variable "availability_domain_index" {
  description = "index into the region's AD list to launch into - apply-retry.sh cycles this across every AD when it hits a capacity error"
  type        = number
  default     = 0
}
