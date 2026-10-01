resource "oci_core_vcn" "worm" {
  compartment_id = var.compartment_id
  cidr_blocks    = [var.vcn_cidr]
  display_name   = "worm-vnic"
  dns_label      = "wormvcn"
}

resource "oci_core_internet_gateway" "worm" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.worm.id
  display_name   = "worm-igw"
  enabled        = true
}

resource "oci_core_route_table" "worm" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.worm.id
  display_name   = "worm-public-rt"

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.worm.id
  }
}

resource "oci_core_security_list" "worm" {
  compartment_id = var.compartment_id
  vcn_id         = oci_core_vcn.worm.id
  display_name   = "worm-public-sl"

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
    stateless   = false
  }

  ingress_security_rules {
    source    = "0.0.0.0/0"
    protocol  = "6"
    stateless = false
    tcp_options {
      min = 22
      max = 22
    }
  }

  ingress_security_rules {
    source    = "0.0.0.0/0"
    protocol  = "6"
    stateless = false
    tcp_options {
      min = 80
      max = 80
    }
  }

  ingress_security_rules {
    source    = "0.0.0.0/0"
    protocol  = "6"
    stateless = false
    tcp_options {
      min = 443
      max = 443
    }
  }

  ingress_security_rules {
    source    = "0.0.0.0/0"
    protocol  = "6"
    stateless = false
    tcp_options {
      min = 8080
      max = 8080
    }
  }
}

resource "oci_core_subnet" "worm" {
  compartment_id             = var.compartment_id
  vcn_id                     = oci_core_vcn.worm.id
  cidr_block                 = var.subnet_cidr
  display_name               = "worm-public-subnet"
  dns_label                  = "wormsub"
  route_table_id             = oci_core_route_table.worm.id
  security_list_ids          = [oci_core_security_list.worm.id]
  prohibit_public_ip_on_vnic = false
}
