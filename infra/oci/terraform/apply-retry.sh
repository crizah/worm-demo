#!/usr/bin/env bash
# terraform has no built-in retry for "out of host capacity" errors, so this
# wraps `terraform apply`: first applies the network resources (vcn, igw,
# route table, security list, subnet - always succeeds, no capacity
# involved), then loops `terraform apply` for the instance across every AD
# in the region, sleeping between passes, until one succeeds.
#
# safe to re-run - terraform apply is idempotent, and a failed instance
# create never lands in state (out-of-capacity means nothing was created).
set -uo pipefail
cd "$(dirname "$0")"

RETRY_SECONDS=60

terraform init -input=false

if terraform state show oci_core_subnet.worm >/dev/null 2>&1; then
    echo "network already provisioned, skipping"
else
    echo "provisioning network..."
    terraform apply -auto-approve -input=false \
        -target=oci_core_vcn.worm \
        -target=oci_core_internet_gateway.worm \
        -target=oci_core_route_table.worm \
        -target=oci_core_security_list.worm \
        -target=oci_core_subnet.worm
fi

AD_COUNT=$(terraform output -raw ad_count)
if [ -z "$AD_COUNT" ] || [ "$AD_COUNT" -lt 1 ]; then
    echo "couldn't determine availability domain count" >&2
    exit 1
fi

attempt=0
while true; do
    attempt=$((attempt + 1))
    for ((i = 0; i < AD_COUNT; i++)); do
        echo "[$(date +%H:%M:%S)] attempt $attempt, AD index $i"
        if terraform apply -auto-approve -input=false -var="availability_domain_index=$i"; then
            echo "instance up:"
            terraform output ssh_command
            exit 0
        fi
        echo "no capacity (AD index $i), waiting ${RETRY_SECONDS}s"
        sleep "$RETRY_SECONDS"
    done
done
