#!/usr/bin/env bash
# Provisions the worm-demo VM from scratch: VCN, public subnet, internet
# gateway, route table, security list - then retries launching the free-tier
# Ampere A1 instance across every AD in the region until Oracle actually has
# capacity for it.
#
# "Out of host capacity" for VM.Standard.A1.Flex is a real, common,
# first-come-first-serve situation on the free tier - capacity opens up when
# other free-tier instances get terminated, not a problem with your account.
#
# Prereqs:
#   - OCI CLI installed + configured (`oci setup config`) - this script
#     reads tenancy/region straight from ~/.oci/config, nothing to fill in
#     there
#   - an SSH public key already downloaded (see SSH_PUBLIC_KEY_FILE below)
#
# NOTE: not run against a live account here (oci CLI isn't installed in
# this sandbox) - the network resource creation calls (route-rules /
# security-list JSON shapes especially) are the parts most likely to need a
# small tweak for your installed CLI version. Everything is idempotent
# (checks by display-name before creating), so it's safe to just fix
# whatever breaks and re-run.
set -uo pipefail

TENANCY_ID=$(awk -F= '/^tenancy=/{print $2}' "$HOME/.oci/config")
COMPARTMENT_ID="$TENANCY_ID"   # root compartment - fine for a personal free-tier tenancy

SSH_PUBLIC_KEY_FILE="$HOME/Downloads/ssh-key-2026-09-30.key.pub"

VCN_NAME="worm-vnic"           # this is the VCN's display name
SUBNET_NAME="worm-public-subnet"
IGW_NAME="worm-igw"
RT_NAME="worm-public-rt"
SL_NAME="worm-public-sl"

DISPLAY_NAME="worm-demo"
SHAPE="VM.Standard.A1.Flex"
OCPUS=1
MEMORY_GB=6
RETRY_SECONDS=60

VCN_CIDR="10.0.0.0/16"
SUBNET_CIDR="10.0.1.0/24"

if [ ! -f "$SSH_PUBLIC_KEY_FILE" ]; then
    echo "ssh public key not found at $SSH_PUBLIC_KEY_FILE" >&2
    exit 1
fi

if [ -z "$COMPARTMENT_ID" ]; then
    echo "couldn't read tenancy from ~/.oci/config" >&2
    exit 1
fi

# --- network setup: idempotent, safe to re-run ---

echo "looking up Canonical Ubuntu 24.04 image for $SHAPE..."
IMAGE_ID=$(oci compute image list \
    --compartment-id "$COMPARTMENT_ID" \
    --operating-system "Canonical Ubuntu" \
    --operating-system-version "24.04" \
    --shape "$SHAPE" \
    --sort-by TIMECREATED --sort-order DESC \
    --query "data[0].id" --raw-output)

if [ -z "$IMAGE_ID" ] || [ "$IMAGE_ID" = "null" ]; then
    echo "couldn't find a Canonical Ubuntu 24.04 image for $SHAPE - run 'oci compute image list --compartment-id $COMPARTMENT_ID --operating-system \"Canonical Ubuntu\"' and check the version string" >&2
    exit 1
fi
echo "image: $IMAGE_ID"

VCN_ID=$(oci network vcn list --compartment-id "$COMPARTMENT_ID" --display-name "$VCN_NAME" --query "data[0].id" --raw-output)
if [ -z "$VCN_ID" ] || [ "$VCN_ID" = "null" ]; then
    echo "creating VCN $VCN_NAME..."
    VCN_ID=$(oci network vcn create \
        --compartment-id "$COMPARTMENT_ID" \
        --display-name "$VCN_NAME" \
        --cidr-block "$VCN_CIDR" \
        --dns-label "wormvcn" \
        --wait-for-state AVAILABLE \
        --query "data.id" --raw-output)
fi
echo "vcn: $VCN_ID"

IGW_ID=$(oci network internet-gateway list --compartment-id "$COMPARTMENT_ID" --vcn-id "$VCN_ID" --display-name "$IGW_NAME" --query "data[0].id" --raw-output)
if [ -z "$IGW_ID" ] || [ "$IGW_ID" = "null" ]; then
    echo "creating internet gateway..."
    IGW_ID=$(oci network internet-gateway create \
        --compartment-id "$COMPARTMENT_ID" \
        --vcn-id "$VCN_ID" \
        --display-name "$IGW_NAME" \
        --is-enabled true \
        --wait-for-state AVAILABLE \
        --query "data.id" --raw-output)
fi
echo "igw: $IGW_ID"

RT_ID=$(oci network route-table list --compartment-id "$COMPARTMENT_ID" --vcn-id "$VCN_ID" --display-name "$RT_NAME" --query "data[0].id" --raw-output)
if [ -z "$RT_ID" ] || [ "$RT_ID" = "null" ]; then
    echo "creating route table (0.0.0.0/0 -> igw)..."
    RT_ID=$(oci network route-table create \
        --compartment-id "$COMPARTMENT_ID" \
        --vcn-id "$VCN_ID" \
        --display-name "$RT_NAME" \
        --route-rules "[{\"destination\": \"0.0.0.0/0\", \"destinationType\": \"CIDR_BLOCK\", \"networkEntityId\": \"$IGW_ID\"}]" \
        --wait-for-state AVAILABLE \
        --query "data.id" --raw-output)
fi
echo "route table: $RT_ID"

SL_ID=$(oci network security-list list --compartment-id "$COMPARTMENT_ID" --vcn-id "$VCN_ID" --display-name "$SL_NAME" --query "data[0].id" --raw-output)
if [ -z "$SL_ID" ] || [ "$SL_ID" = "null" ]; then
    echo "creating security list (ssh/80/443/8080 ingress, all egress)..."
    SL_ID=$(oci network security-list create \
        --compartment-id "$COMPARTMENT_ID" \
        --vcn-id "$VCN_ID" \
        --display-name "$SL_NAME" \
        --egress-security-rules '[{"destination": "0.0.0.0/0", "protocol": "all", "isStateless": false}]' \
        --ingress-security-rules '[
            {"source": "0.0.0.0/0", "protocol": "6", "isStateless": false, "tcpOptions": {"destinationPortRange": {"min": 22, "max": 22}}},
            {"source": "0.0.0.0/0", "protocol": "6", "isStateless": false, "tcpOptions": {"destinationPortRange": {"min": 80, "max": 80}}},
            {"source": "0.0.0.0/0", "protocol": "6", "isStateless": false, "tcpOptions": {"destinationPortRange": {"min": 443, "max": 443}}},
            {"source": "0.0.0.0/0", "protocol": "6", "isStateless": false, "tcpOptions": {"destinationPortRange": {"min": 8080, "max": 8080}}}
        ]' \
        --wait-for-state AVAILABLE \
        --query "data.id" --raw-output)
fi
echo "security list: $SL_ID"

SUBNET_ID=$(oci network subnet list --compartment-id "$COMPARTMENT_ID" --vcn-id "$VCN_ID" --display-name "$SUBNET_NAME" --query "data[0].id" --raw-output)
if [ -z "$SUBNET_ID" ] || [ "$SUBNET_ID" = "null" ]; then
    echo "creating public subnet..."
    SUBNET_ID=$(oci network subnet create \
        --compartment-id "$COMPARTMENT_ID" \
        --vcn-id "$VCN_ID" \
        --display-name "$SUBNET_NAME" \
        --cidr-block "$SUBNET_CIDR" \
        --dns-label "wormsub" \
        --route-table-id "$RT_ID" \
        --security-list-ids "[\"$SL_ID\"]" \
        --prohibit-public-ip-on-vnic false \
        --wait-for-state AVAILABLE \
        --query "data.id" --raw-output)
fi
echo "subnet: $SUBNET_ID"

# --- instance: retry loop across ADs until capacity is available ---

ADS=$(oci iam availability-domain list -c "$COMPARTMENT_ID" --query "data[].name" --raw-output 2>/dev/null | tr -d '[]"' | tr ',' ' ')
if [ -z "$ADS" ]; then
    echo "couldn't list availability domains - check your OCI CLI config" >&2
    exit 1
fi

attempt=0
while true; do
    attempt=$((attempt + 1))
    for ad in $ADS; do
        echo "[$(date +%H:%M:%S)] attempt $attempt, AD: $ad"
        INSTANCE_ID=$(oci compute instance launch \
            --compartment-id "$COMPARTMENT_ID" \
            --availability-domain "$ad" \
            --shape "$SHAPE" \
            --shape-config "{\"ocpus\": $OCPUS, \"memoryInGBs\": $MEMORY_GB}" \
            --subnet-id "$SUBNET_ID" \
            --image-id "$IMAGE_ID" \
            --display-name "$DISPLAY_NAME" \
            --ssh-authorized-keys-file "$SSH_PUBLIC_KEY_FILE" \
            --assign-public-ip true \
            --wait-for-state RUNNING \
            --max-wait-seconds 120 \
            --query "data.id" --raw-output)
        if [ -n "$INSTANCE_ID" ] && [ "$INSTANCE_ID" != "null" ]; then
            echo "launched: $INSTANCE_ID in $ad"
            PUBLIC_IP=$(oci compute instance list-vnics \
                --compartment-id "$COMPARTMENT_ID" \
                --instance-id "$INSTANCE_ID" \
                --query "data[0].\"public-ip\"" --raw-output)
            echo "public ip: $PUBLIC_IP"
            echo "ssh -i ~/Downloads/<matching-private-key> ubuntu@$PUBLIC_IP"
            exit 0
        fi
        echo "no capacity in $ad, waiting ${RETRY_SECONDS}s"
        sleep "$RETRY_SECONDS"
    done
done
