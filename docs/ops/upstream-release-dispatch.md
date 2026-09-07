# Upstream Release Dispatch

How an upstream project gets its releases onto Packyard automatically.
The upstream repository publishes a GitHub Release on a version tag and, as its last release step, triggers `promote-release.yml` in this repository.
Packyard then pulls the assets and images from public sources.
See [Promotion pipeline](../reference/promotion-pipeline.md) for what the workflow does.

## What the upstream release must provide

- A GitHub Release on the tag with the `.rpm` and `.deb` packages attached.
- A `SHA256SUMS` file covering those packages, plus `SHA256SUMS.sig` and `SHA256SUMS.pem` from `cosign sign-blob` run by the release workflow.
- Images pushed to a registry as `<source_registry>/<image>:<version>` and signed keylessly by the same release workflow.
- `<version>` equals the tag without its `v` prefix.

Packyard verifies the checksums signature and every image against the identity `https://github.com/<source_repo>/.github/workflows/<source_workflow>@refs/tags/<tag>` before touching anything.

## One-time setup

### 1. Provision the component

Create the component in the admin UI and give it the RPM series and OS targets the promotion will publish into.
The auth service creates the directory tree from that record.
A promotion into a target that is not on the component fails with a message naming `rpm_os_families`.

For Bluebird: component `bluebird`, `rpm_series ["38"]`, `rpm_os_families ["el9","el10"]`, `rpm_architectures ["x86_64"]`, visibility `public`.

### 2. Create the dispatch token

A fine-grained personal access token owned by a Packyard maintainer:

- Repository access: only `no42-org/packyard`.
- Permission: **Actions: Read and write**. Nothing else.
- Expiry: one year. Put the date in a calendar. An expired token fails the upstream release job with `401`, loudly.

The token can start workflows in this repository and nothing more.
It cannot read secrets, and it cannot reach the package host.

Store it in the upstream repository as the secret `PACKYARD_DISPATCH_TOKEN`.

A GitHub App is the cleaner long-term replacement once the upstream project triggers more than one thing here.
It is parked until then.

### 3. Add the dispatch job upstream

Append a job to the upstream release workflow that runs only on version tags, after the release and the image pushes have succeeded:

```yaml
  promote-to-packyard:
    if: startsWith(github.ref, 'refs/tags/v')
    needs: [release, core-oci, minion-oci, sentinel-oci]
    runs-on: ubuntu-24.04
    timeout-minutes: 5
    permissions: {}
    steps:
      - name: Trigger Packyard promotion
        env:
          GH_TOKEN: ${{ secrets.PACKYARD_DISPATCH_TOKEN }}
          TAG: ${{ github.ref_name }}
        run: |
          gh workflow run promote-release.yml -R no42-org/packyard \
            -f source_repo="${GITHUB_REPOSITORY}" \
            -f tag="${TAG}" \
            -f component=bluebird \
            -f series=38
```

Adjust `needs` to the job names that produce the release and the images.
The other inputs keep their defaults, which are listed in the pipeline reference.

## Watching a promotion

```bash
gh run list -R no42-org/packyard --workflow=promote-release.yml --limit 5
gh run watch -R no42-org/packyard <run-id>
```

The last step curls every published path through the public hostname and prints the status codes.

## Re-running or rolling back

Dispatch the workflow by hand with the same inputs to repeat a promotion, or with an older `tag` to point the series at an earlier release:

```bash
gh workflow run promote-release.yml -R no42-org/packyard \
  -f source_repo=Bluebird-Community/opennms -f tag=v38.1.0 -f component=bluebird -f series=38
```

Packages of both versions stay in the repositories; the floating `:<series>` image tag moves to the version last promoted.
