# Package Promotion Runbook

How to promote staged LTS packages (RPM, DEB, OCI) through the signing pipeline and into the serving infrastructure.

**Last updated:** 2026-04-14

---

## Overview

Promotion is always manual — one `workflow_dispatch` per component and target. The workflows download from RustFS staging, verify checksums, sign, and publish directly to the serving stack on the VM.

| Format | Workflow | Key input parameters |
|--------|----------|---------------------|
| RPM | `promote-rpm.yml` | `component`, `series`, `os` |
| DEB | `promote-deb.yml` | `component`, `series`, `distro` |
| OCI | `promote-oci.yml` | `component`, `series` |

---

## 1. Pre-promotion checklist

- [ ] `static/content/gpg/lts.asc` is a real GPG public key (not a placeholder)
- [ ] `static/content/gpg/cosign.pub` is a real cosign public key (not a placeholder)
- [ ] All 10 GitHub Actions secrets are set (see [Production Deployment §3](production-deployment.md#3-secrets--environment))
- [ ] Artifacts are staged in RustFS (§2 below)

---

## 2. Stage artifacts

RustFS is internal-only. Open an SSH tunnel before staging:

```bash
ssh -L 9000:localhost:9000 deploy@pkg.example.org -N &
```

Set credentials and call `stage-artifact.sh` for each artifact. The script uploads the file and a paired `.sha256` checksum:

Component names must match those defined in `config/packyard.yml`. The examples below assume the default three-component setup — adjust as needed.

```bash
export RUSTFS_ENDPOINT=http://localhost:9000
export RUSTFS_ACCESS_KEY=<key>
export RUSTFS_SECRET_KEY=<secret>

# RPM — one call per component × os combination
bash scripts/stage-artifact.sh core     2025 rpm el9-x86_64  ./packyard-core-2025.el9.x86_64.rpm
bash scripts/stage-artifact.sh minion   2025 rpm el9-x86_64  ./packyard-minion-2025.el9.x86_64.rpm
bash scripts/stage-artifact.sh sentinel 2025 rpm el9-x86_64  ./packyard-sentinel-2025.el9.x86_64.rpm

# DEB — os-arch is {distro}-amd64
bash scripts/stage-artifact.sh core 2025 deb noble-amd64 ./packyard-core-2025_amd64.deb

# OCI — stage x86_64 and arm64 archives separately
bash scripts/stage-artifact.sh core 2025 oci x86_64 ./lts-core-x86_64.tar
bash scripts/stage-artifact.sh core 2025 oci arm64  ./lts-core-arm64.tar
```

Confirm the artifacts are in the bucket:

```bash
AWS_ACCESS_KEY_ID=$RUSTFS_ACCESS_KEY \
AWS_SECRET_ACCESS_KEY=$RUSTFS_SECRET_KEY \
  aws s3 ls s3://staging/ \
  --endpoint-url http://localhost:9000 \
  --region us-east-1 \
  --recursive
```

---

## 3. Promote

Promotions are serialised per component/target by GitHub's concurrency groups — running multiple dispatches at once is safe.

### RPM

```bash
gh workflow run promote-rpm.yml \
  -f component=core \
  -f series=2025 \
  -f os=el9-x86_64
```

Valid `os` values: `el8-x86_64`, `el9-x86_64`, `el10-x86_64`, `centos10-x86_64`

### DEB

```bash
gh workflow run promote-deb.yml \
  -f component=core \
  -f series=2025 \
  -f distro=noble
```

Valid `distro` values: `bookworm`, `trixie`, `jammy`, `noble`

### OCI

```bash
gh workflow run promote-oci.yml \
  -f component=core \
  -f series=2025
```

OCI promotion builds the multi-arch index from both staged architectures and cosign-signs all manifests in a single run — no per-arch dispatch needed.

Monitor runs:

```bash
gh run list --workflow=promote-rpm.yml
gh run list --workflow=promote-deb.yml
gh run list --workflow=promote-oci.yml
```

---

## 4. Verify

Confirm promoted packages are reachable with a valid subscriber key:

```bash
# RPM repodata
curl -sI -u subscriber:<KEY> \
  https://pkg.example.org/rpm/core/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 200

# DEB InRelease
curl -sI -u subscriber:<KEY> \
  https://pkg.example.org/deb/core/2025/dists/noble/InRelease
# Expect: HTTP/2 200
```

For a full stack check, run the verification suite: [Production Deployment §10](production-deployment.md#10-run-the-verification-suite).
