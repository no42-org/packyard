# Releasing Packyard

This covers cutting a new version of the Packyard infrastructure itself: the auth, rpm and static service images plus the versioned docs.
Versions follow [SemVer](https://semver.org/) and are derived from the Conventional Commit types since the last tag.
A `feat` commit means a minor bump, a `fix` or `chore` a patch, and a `BREAKING CHANGE` footer or `!` a major bump.

## 1. Bump version numbers

Four places must match the intended release tag (without the `v` prefix):

```bash
VERSION=1.2.3
sed -i "s/const version = .*/const version = \"${VERSION}\"/" auth/cmd/server/version.go
echo "${VERSION}" > rpm/VERSION
echo "${VERSION}" > static/VERSION
sed -i -E "s|(packyard-(auth\|rpm\|static)):[^ ]*|\1:${VERSION}|" compose.yml
```

Commit as `chore(release): vX.Y.Z`, open a PR and merge it.
`main` is protected, so the bump always lands through a PR.

## 2. Tag and push

```bash
git checkout main && git pull
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

Pushing the tag triggers the `Release` workflow (`.github/workflows/release.yml`). It:

1. Runs every quality gate (`quality-gates.yml` with `all: true`). Nothing publishes on red.
2. Builds and pushes the three images for `linux/amd64` and `linux/arm64`.
3. Signs each image with cosign keyless and attaches SLSA build provenance to the registry.
4. Generates an SPDX SBOM per image.
5. Creates a **draft** GitHub Release with the SBOMs, a `checksums.txt` and its cosign signature attached, and dispatches `docs.yml` so the landing page shows the new version.

| Image | Tags written for `v1.2.3` |
|-------|---------------------------|
| `ghcr.io/no42-org/packyard-auth` | `1.2.3`, `1.2`, `latest` |
| `ghcr.io/no42-org/packyard-rpm` | `1.2.3`, `1.2`, `latest` |
| `ghcr.io/no42-org/packyard-static` | `1.2.3`, `1.2`, `latest` |

`latest` always equals the newest stable release.
Prerelease tags such as `v1.2.3-rc1` publish `1.2.3-rc1`, are marked as prereleases and never move `latest`.

Monitor the run:

```bash
gh run list --workflow=release.yml
gh run watch
```

## 3. Publish the release

Write curated notes, never a raw commit dump: a `## Highlights` section with user-facing impact, `## Breaking changes` with the migration path if any, `## Fixes` one line each, and one line summarizing dependency updates.
Then publish the draft:

```bash
gh release edit v1.2.3 --notes-file notes.md --draft=false
```

Confirm the landing page at https://no42-org.github.io/packyard/ shows the new version.

## 4. Bump to the next development version

Bump `main` to the next patch version with an `-rc` suffix so preview builds are never mistaken for a release:

```bash
NEXT=1.2.4-rc
git checkout -b chore/bump-${NEXT}
sed -i "s/const version = .*/const version = \"${NEXT}\"/" auth/cmd/server/version.go
echo "${NEXT}" > rpm/VERSION
echo "${NEXT}" > static/VERSION
sed -i -E "s|(packyard-(auth\|rpm\|static)):[^ ]*|\1:${NEXT}|" compose.yml
git add auth/cmd/server/version.go rpm/VERSION static/VERSION compose.yml
git commit -s -m "chore: bump versions to ${NEXT} post v1.2.3 release"
git push -u origin chore/bump-${NEXT}
gh pr create --fill
```

## Preview images from main

Every push to `main` that touches a service runs the quality gates and then pushes a single mutable preview tag named after the `-rc` version in the source tree, for example `ghcr.io/no42-org/packyard-auth:1.2.4-rc`.
Each build overwrites the previous one.
Preview images are signed with cosign but never receive `X.Y.Z`, `X.Y` or `latest` tags.

## Verifying signatures and provenance

Images are signed keylessly by the `Release` workflow. Verify against the workflow identity:

```bash
cosign verify ghcr.io/no42-org/packyard-auth:1.2.3 \
  --certificate-identity-regexp 'https://github.com/no42-org/packyard/\.github/workflows/release\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Preview images are signed by the `Preview <service> image` workflows; use `build-auth\.yml@refs/heads/main` (or `build-rpm`, `build-static`) as the identity instead.

Verify SLSA build provenance for an image or a downloaded release asset:

```bash
gh attestation verify oci://ghcr.io/no42-org/packyard-auth:1.2.3 --owner no42-org
gh attestation verify checksums.txt --owner no42-org
```

Verify the release checksums file:

```bash
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/no42-org/packyard/\.github/workflows/release\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c checksums.txt
```
