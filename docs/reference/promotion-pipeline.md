# Promotion Pipeline

Packages reach the serving stack through GitHub Actions workflows in this repository.
There are two entry points: an automated one that ingests an upstream GitHub Release, and a manual one that promotes files staged by hand.

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `promote-release.yml` | `workflow_dispatch`, normally called by the upstream release job | Downloads an upstream GitHub Release, verifies it, signs and publishes RPMs and DEBs, copies and signs images. One run per release |
| `promote-rpm.yml` | `workflow_dispatch` | Downloads RPMs from RustFS staging, GPG-signs them, publishes into the rpm container and rebuilds repodata |
| `promote-deb.yml` | `workflow_dispatch` | Downloads DEBs from RustFS staging, GPG-signs them, creates an Aptly snapshot and publishes it |
| `promote-oci.yml` | `workflow_dispatch` | Downloads per-architecture OCI tarballs from RustFS staging, pushes a multi-arch index to Zot and signs it keylessly |

## Automated: promote an upstream release

The upstream project publishes a GitHub Release on a version tag with its packages, a `SHA256SUMS` file signed with cosign, and images in its own registry.
Its release job then triggers `promote-release.yml` here with one API call.
Packyard pulls everything from public sources, so the upstream repository holds no SSH key and no storage credential.

```text
upstream repo, tag v38.1.0                     no42-org/packyard, promote-release.yml
──────────────────────────────                 ───────────────────────────────────────────────
GitHub Release                                 1. validate inputs, preflight the host over SSH
  bbo-core-38.1.0-761.x86_64.rpm   ──────────► 2. gh release download, cosign verify-blob SHA256SUMS,
  bbo-core_38.1.0-761_amd64.deb                   sha256sum --check
  SHA256SUMS, SHA256SUMS.sig/.pem               3. rpmsign with the Packyard GPG key
quay.io/bluebird/core:38.1.0      ──────────► 4. rpm container:  add-package.sh into every rpm_target
  (cosign-signed)                              5. aptly container: one snapshot, published per deb_distro
                                               6. cosign verify upstream, crane copy into Zot as
                                                  lts-<component>/<image>:<version> and :<series>,
                                                  cosign sign with this workflow's identity
                                               7. curl every published path through the public hostname
```

Inputs:

| Input | Meaning | Example |
|-------|---------|---------|
| `source_repo` | Upstream repository | `Bluebird-Community/opennms` |
| `tag` | Release tag to promote | `v38.1.0` |
| `component` | Packyard component, must exist | `bluebird` |
| `series` | Series label under the component | `38` |
| `rpm_targets` | Comma-separated os-arch targets, each provisioned on the component | `el9-x86_64,el10-x86_64` |
| `deb_distros` | Comma-separated distributions | `bookworm,trixie,jammy,noble` |
| `images` | Comma-separated image names, empty to skip | `core,minion,sentinel` |
| `source_registry` | Where upstream images live | `quay.io/bluebird` |
| `source_workflow` | Upstream workflow that signed the release | `main.yml` |

The version is the tag without its `v` prefix.
Re-running the workflow for the same tag is safe: files are overwritten with identical content, Aptly skips packages it already holds, and image digests do not change.

Wiring the upstream side is described in [Upstream release dispatch](../ops/upstream-release-dispatch.md).

## Manual: stage and promote by hand

For one-off promotions that do not come from a GitHub Release, stage files into RustFS and dispatch the format-specific workflow.
The `component` argument must match a name provisioned via `POST /api/v1/components`.
`series` is any path-safe label, for example an LTS year (`2025`) or a major version (`38`).

```bash
RUSTFS_ACCESS_KEY=... RUSTFS_SECRET_KEY=... \
  bash scripts/stage-artifact.sh core 2025 rpm el9-x86_64 /path/to/artifact.rpm
```

Then trigger the corresponding promotion workflow with `component`, `series`, and `os` inputs via the GitHub Actions UI or CLI:

```bash
gh workflow run promote-rpm.yml \
  -f component=core \
  -f series=2025 \
  -f os=el9-x86_64
```

The step-by-step procedure is in the [release runbook](../ops/release-runbook.md).

## What every promotion has in common

- **Packyard signs, upstream signatures are only checked.** RPMs carry the Packyard GPG key; DEB repositories carry it on their `InRelease`, which is what apt verifies (individual `.deb` files are not signed, apt never checks those). Images are signed keylessly by the promoting workflow's GitHub OIDC identity. Subscribers verify one identity for everything on the host.
- **The GPG key must be the served key.** Before signing, the workflows compare the `GPG_KEY_ID` fingerprint with the key served at `https://<host>/gpg/lts.asc`. The copy of `lts.asc` in this repository is a placeholder.
- **Publishing happens inside the service containers, through one set of scripts.** `scripts/publish/rpm.sh`, `deb.sh` and `oci.sh` are the only code that publishes. The workflows copy them to the host and stream packages into them over SSH; `make test-publish-scripts` runs the same scripts against a hardened local stack. The rpm image ships `createrepo_c`, Aptly runs in its own container, and the host needs Docker and the `deploy` user, nothing else. See [Production deployment §5](../ops/production-deployment.md#5-creating-the-deploy-user).
- **One copy per package.** A package published into several RPM targets is hardlinked between the target directories. Aptly stores each `.deb` once in its pool no matter how many distributions publish it.
- **Concurrency groups serialise per component and target.** Dispatching several promotions at once is safe.
- **CI runs the subscriber paths on every stack change.** The integration workflow publishes fixtures through the same scripts and runs the RPM, DEB and OCI subscriber tests, including an assertion that anonymous writes to a public OCI component are refused.
