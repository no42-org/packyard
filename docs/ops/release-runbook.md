# Package Promotion Runbook

How to promote staged LTS packages (RPM, DEB, OCI) through the signing pipeline and into the serving infrastructure.

**Last updated:** 2026-09-07

---

## Overview

There are two ways into the serving stack.

**Automated.** An upstream project's release job triggers `promote-release.yml`, which pulls the GitHub Release and the images, verifies them, signs everything with Packyard's keys and publishes all three formats in one run.
Nothing to stage, nothing to dispatch by hand.
Setting it up is described in [Upstream release dispatch](upstream-release-dispatch.md); the same workflow can be dispatched by hand to repeat or roll back a release:

```bash
gh workflow run promote-release.yml \
  -f source_repo=Bluebird-Community/opennms \
  -f tag=v38.1.0 \
  -f component=bluebird \
  -f series=38
```

**Manual.** For files that do not come from a GitHub Release, stage them into RustFS and run one `workflow_dispatch` per component and target.
The rest of this runbook covers the manual path.

| Format | Workflow | Key input parameters |
|--------|----------|---------------------|
| RPM | `promote-rpm.yml` | `component`, `series`, `os` |
| DEB | `promote-deb.yml` | `component`, `series`, `distro` |
| OCI | `promote-oci.yml` | `component`, `series` |

`series` is any path-safe label under the component: an LTS year such as `2025`, or a major version such as `38`.
The RPM target directories come from the component record (`rpm_series`, `rpm_os_families`, `rpm_architectures`); a promotion into an unprovisioned target fails and says so.

---

## 1. Pre-promotion checklist

- [ ] `https://<host>/gpg/lts.asc` serves the public key whose fingerprint is the `GPG_KEY_ID` secret (the workflows check this before signing)
- [ ] The component and, for RPM, its series and OS targets exist (admin UI or `POST /api/v1/components`)
- [ ] The host contract is in place: `DEPLOY_DIR` variable, `COMPOSE_PROJECT_NAME` and a `deploy`-readable `.env` (see [Production Deployment §5](production-deployment.md#5-creating-the-deploy-user))
- [ ] All 8 GitHub Actions secrets are set (see [Production Deployment §3](production-deployment.md#3-secrets--environment))
- [ ] Artifacts are staged in RustFS (§2 below)

---

## 2. Stage artifacts

RustFS is internal-only. Open an SSH tunnel before staging:

```bash
ssh -L 9000:localhost:9000 deploy@pkg.example.org -N &
```

Set credentials and call `stage-artifact.sh` for each artifact. The script uploads the file and a paired `.sha256` checksum:

Component names must match those provisioned via `POST /api/v1/components`. The examples below assume the default three-component setup — adjust as needed.

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
bash scripts/stage-artifact.sh core 2025 oci x86_64 ./core-x86_64.tar
bash scripts/stage-artifact.sh core 2025 oci arm64  ./core-arm64.tar
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

OCI promotion builds the multi-arch index from both staged architectures and signs all manifests keylessly with cosign in a single run — no per-arch dispatch needed.

Monitor runs:

```bash
gh run list --workflow=promote-rpm.yml
gh run list --workflow=promote-deb.yml
gh run list --workflow=promote-oci.yml
```

### Changing component visibility

Forward-auth caches `public` visibility for `PACKYARD_PUBLIC_COMPONENT_CACHE_TTL` (30s unless set in `.env`). Changing a component from private to public takes effect on the next request. Changing it from public to private through `PATCH /api/v1/components/{name}` takes effect immediately on the auth instance that handled the request, which on the shipped single-instance Compose stack is the only instance. The cache can still serve the old answer for up to one TTL when the change did not go through that API: a second auth instance, a direct edit of the SQLite database, or a restore of the `auth-db` volume while the service is running. Before promoting content that must not be public into a component you have just made private:

1. Change the visibility: `PATCH /api/v1/components/{name}` with `{"visibility": "private"}`.
2. Wait one TTL. This is a no-op safety margin on a single instance and required in the cases above.
3. Promote.

To take the cache out of the picture entirely, set `PACKYARD_PUBLIC_COMPONENT_CACHE_TTL=0` in `.env` and recreate the container with `docker compose up -d auth`. A plain `docker compose restart` keeps the old environment and will not apply the change. No image change is needed.

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
