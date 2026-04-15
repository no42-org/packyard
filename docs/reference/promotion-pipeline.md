# Promotion Pipeline

Packages are promoted from staging to serving via GitHub Actions:

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `promote-rpm.yml` | `workflow_dispatch` | Downloads RPM from RustFS staging, GPG-signs it, rebuilds repodata, publishes to rpm service |
| `promote-deb.yml` | `workflow_dispatch` | Downloads DEB from RustFS staging, GPG-signs it, creates Aptly snapshot, publishes to deb service |
| `promote-oci.yml` | `workflow_dispatch` | Downloads OCI tarballs, pushes multi-arch image index to Zot, cosign-signs all manifests |

## Staging an artifact

The `component` argument must match a name defined in `config/packyard.yml`.

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
