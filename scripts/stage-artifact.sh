#!/usr/bin/env bash
# stage-artifact.sh — upload an unsigned artefact to RustFS staging
# Usage: stage-artifact.sh <component> <year> <format> <os-arch> <local-file>
#
# Required env vars:
#   RUSTFS_ENDPOINT     — e.g. http://localhost:9000 (via SSH tunnel: ssh -L 9000:localhost:9000 deploy@HOST)
#   RUSTFS_ACCESS_KEY   — RustFS S3 access key
#   RUSTFS_SECRET_KEY   — RustFS S3 secret key
#   RUSTFS_BUCKET       — (optional) bucket name, defaults to "staging"
#
# Uploads artifact + SHA256 checksum to:
#   s3://{bucket}/{component}/{year}/{format}/{os-arch}/{filename}
#   s3://{bucket}/{component}/{year}/{format}/{os-arch}/{filename}.sha256
set -euo pipefail

COMPONENT="${1:?component required (core|minion|sentinel)}"
YEAR="${2:?year required (e.g. 2025)}"
FORMAT="${3:?format required (rpm|deb|oci)}"
OS_ARCH="${4:?os-arch required (e.g. el9-x86_64)}"
LOCAL_FILE="${5:?local file path required}"

: "${RUSTFS_ENDPOINT:?RUSTFS_ENDPOINT must be set (e.g. http://localhost:9000 via SSH tunnel)}"
: "${RUSTFS_ACCESS_KEY:?RUSTFS_ACCESS_KEY must be set}"
: "${RUSTFS_SECRET_KEY:?RUSTFS_SECRET_KEY must be set}"
BUCKET="${RUSTFS_BUCKET:-staging}"

# Validate component
case "${COMPONENT}" in
  core|minion|sentinel) ;;
  *) echo "ERROR: component must be one of: core, minion, sentinel (got: ${COMPONENT})"; exit 1 ;;
esac

# Validate format
case "${FORMAT}" in
  rpm|deb|oci) ;;
  *) echo "ERROR: format must be one of: rpm, deb, oci (got: ${FORMAT})"; exit 1 ;;
esac

[ -f "${LOCAL_FILE}" ] || { echo "ERROR: file not found: ${LOCAL_FILE}"; exit 1; }

FILENAME=$(basename "${LOCAL_FILE}")
S3_KEY="${COMPONENT}/${YEAR}/${FORMAT}/${OS_ARCH}/${FILENAME}"

# Generate SHA256 checksum
echo "Generating checksum for ${FILENAME}..."
CHECKSUM_FILE="${LOCAL_FILE}.sha256"
sha256sum "${LOCAL_FILE}" | awk '{print $1}' > "${CHECKSUM_FILE}"
echo "  SHA256: $(cat "${CHECKSUM_FILE}")"

# Upload artifact
echo "Uploading ${FILENAME} to s3://${BUCKET}/${S3_KEY}..."
AWS_ACCESS_KEY_ID="${RUSTFS_ACCESS_KEY}" \
AWS_SECRET_ACCESS_KEY="${RUSTFS_SECRET_KEY}" \
  aws s3 cp "${LOCAL_FILE}" "s3://${BUCKET}/${S3_KEY}" \
  --endpoint-url "${RUSTFS_ENDPOINT}" \
  --region us-east-1

# Upload checksum
echo "Uploading checksum ${FILENAME}.sha256..."
AWS_ACCESS_KEY_ID="${RUSTFS_ACCESS_KEY}" \
AWS_SECRET_ACCESS_KEY="${RUSTFS_SECRET_KEY}" \
  aws s3 cp "${CHECKSUM_FILE}" "s3://${BUCKET}/${S3_KEY}.sha256" \
  --endpoint-url "${RUSTFS_ENDPOINT}" \
  --region us-east-1

echo "Done: staged ${FILENAME} and checksum to s3://${BUCKET}/${S3_KEY}"
