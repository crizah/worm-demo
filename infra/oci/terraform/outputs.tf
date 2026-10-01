output "ad_count" {
  value = length(data.oci_identity_availability_domains.ads.availability_domains)
}

output "public_ip" {
  value = oci_core_instance.worm_demo.public_ip
}

output "ssh_command" {
  value = "ssh ubuntu@${oci_core_instance.worm_demo.public_ip}"
}
