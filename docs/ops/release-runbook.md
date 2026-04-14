# Release Runbook

**Last updated:** 2026-04-14

## What is automatic vs manual

| Step | Trigger | Workflow |
|------|---------|----------|
| Build and push auth image | Tag `v*.*.*` | `build-auth.yml` |
| Build and push rpm image | Tag `v*.*.*` | `build-rpm.yml` |
| Build and push static image | Tag `v*.*.*` | `build-static.yml` |
| Deploy versioned docs | GitHub release published | `docs.yml` |
| Promote RPM packages | `workflow_dispatch` | `promote-rpm.yml` |
| Promote DEB packages | `workflow_dispatch` | `promote-deb.yml` |
| Promote OCI images | `workflow_dispatch` | `promote-oci.yml` |

Tagging triggers image builds. Publishing the GitHub release deploys the docs. Package promotions are always manual — one dispatch per component and target.

---

## 1. Pre-release checklist

- [ ] `auth/cmd/server/version.go` — version string matches the intended release tag (without the `v` prefix)
- [ ] `rpm/VERSION` — same version string
- [ ] `static/content/gpg/lts.asc` is a real GPG public key block, not a placeholder
- [ ] `static/content/gpg/cosign.pub` is a real cosign public key, not a placeholder
- [ ] All 10 GitHub Actions secrets are set (see [Production Deployment §3](production-deployment.md#3-secrets--environment))
- [ ] Artifacts to be promoted are staged in RustFS (§3 below)
- [ ] All PRs for this release are merged to `main`

---

## 2. Tag and create the GitHub release

```bash
git checkout main && git pull

git tag v1.2.3
git push origin v1.2.3
```

Pushing the tag immediately triggers `build-auth.yml`, `build-rpm.yml`, and `build-static.yml`. Watch them in the Actions tab — they must all succeed before creating the release.

Once the image builds pass, create the GitHub release:

```bash
gh release create v1.2.3 \
  --title "v1.2.3" \
  --generate-notes
```

Publishing the release triggers `docs.yml`, which runs `mike deploy v1.2.3 latest` and pushes to `gh-pages`. Docs are live at `https://no42-org.github.io/packyard/` within a few minutes.

---

## 3. Stage artifacts

Artifacts must be in RustFS staging before running the promotion workflows. Stage them from a machine with the RustFS credentials — the endpoint is internal-only, so open an SSH tunnel first:

```bash
ssh -L 9000:localhost:9000 deploy@pkg.example.org -N &
```

Then stage each artifact. The script uploads the file and a paired `.sha256`:

```bash
export RUSTFS_ENDPOINT=http://localhost:9000
export RUSTFS_ACCESS_KEY=<key>
export RUSTFS_SECRET_KEY=<secret>

# RPM — one call per component/os combination
bash scripts/stage-artifact.sh core    2025 rpm el9-x86_64  ./packyard-core-2025.el9.x86_64.rpm
bash scripts/stage-artifact.sh minion  2025 rpm el9-x86_64  ./packyard-minion-2025.el9.x86_64.rpm
bash scripts/stage-artifact.sh sentinel 2025 rpm el9-x86_64 ./packyard-sentinel-2025.el9.x86_64.rpm

# DEB — os-arch is {distro}-amd64
bash scripts/stage-artifact.sh core 2025 deb noble-amd64 ./packyard-core-2025_amd64.deb

# OCI — stage x86_64 and arm64 archives separately
bash scripts/stage-artifact.sh core 2025 oci x86_64 ./lts-core-x86_64.tar
bash scripts/stage-artifact.sh core 2025 oci arm64  ./lts-core-arm64.tar
```

Confirm the artifacts landed in the expected bucket paths:

```bash
AWS_ACCESS_KEY_ID=$RUSTFS_ACCESS_KEY \
AWS_SECRET_ACCESS_KEY=$RUSTFS_SECRET_KEY \
  aws s3 ls s3://staging/ \
  --endpoint-url http://localhost:9000 \
  --region us-east-1 \
  --recursive
```

---

## 4. Promote packages

Trigger one workflow per component and target. Promotions are serialised per component/target by GitHub's concurrency groups — running multiple at once is safe.

### RPM

```bash
# Repeat for each component × os combination that has staged artifacts
gh workflow run promote-rpm.yml \
  -f component=core \
  -f year=2025 \
  -f os=el9-x86_64
```

Valid `os` values: `el8-x86_64`, `el9-x86_64`, `el10-x86_64`, `centos10-x86_64`

### DEB

```bash
gh workflow run promote-deb.yml \
  -f component=core \
  -f year=2025 \
  -f distro=noble
```

Valid `distro` values: `bookworm`, `trixie`, `jammy`, `noble`

### OCI

```bash
gh workflow run promote-oci.yml \
  -f component=core \
  -f year=2025
```

OCI promotion builds the multi-arch index from both staged architectures and cosign-signs all manifests in a single run — no per-arch dispatch needed.

Monitor each workflow run:

```bash
gh run list --workflow=promote-rpm.yml
gh run list --workflow=promote-deb.yml
gh run list --workflow=promote-oci.yml
```

---

## 5. Verify

Run the verification suite from [Production Deployment §10](production-deployment.md#10-run-the-verification-suite).

Check that promoted packages are reachable with a valid subscriber key:

```bash
# RPM repodata must be present
curl -sI -u subscriber:<KEY> \
  https://pkg.example.org/rpm/core/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 200

# DEB InRelease must be present
curl -sI -u subscriber:<KEY> \
  https://pkg.example.org/deb/core/2025/dists/noble/InRelease
# Expect: HTTP/2 200
```
