# Releasing Packyard

This covers cutting a new version of the Packyard infrastructure itself — the auth service, RPM service, and static service images, plus the versioned docs.

## 1. Bump version numbers

Three places must match the intended release tag (without the `v` prefix):

```bash
sed -i "s/const version = .*/const version = \"1.2.3\"/" auth/cmd/server/version.go
echo "1.2.3" > rpm/VERSION
echo "1.2.3" > static/VERSION

sed -i "s|packyard-auth:[^ ]*|packyard-auth:1.2.3|" compose.yml
sed -i "s|packyard-rpm:[^ ]*|packyard-rpm:1.2.3|" compose.yml
sed -i "s|packyard-static:[^ ]*|packyard-static:1.2.3|" compose.yml
```

Open a PR and merge to `main` before tagging.

## 2. Tag and push

```bash
git checkout main && git pull

git tag v1.2.3
git push origin v1.2.3
```

Pushing the tag immediately triggers three image build workflows:

| Workflow | Image pushed |
|----------|-------------|
| `build-auth.yml` | `ghcr.io/no42-org/packyard-auth:1.2.3` + `latest` |
| `build-rpm.yml` | `ghcr.io/no42-org/packyard-rpm:1.2.3` + `latest` |
| `build-static.yml` | `ghcr.io/no42-org/packyard-static:1.2.3` + `latest` |

Monitor them:

```bash
gh run list --workflow=build-auth.yml
gh run list --workflow=build-rpm.yml
gh run list --workflow=build-static.yml
```

All three must succeed before creating the GitHub release.

## 3. Create the GitHub release

```bash
gh release create v1.2.3 \
  --title "v1.2.3" \
  --generate-notes
```

Publishing the release triggers a rebuild of `docs.yml` via the `workflow_run` hook, so the landing-page version eyebrow (resolved from `git describe --tags` in `docusaurus.config.ts`) picks up the new tag. Docs themselves publish on every push to `main`, not on release — so by the time you cut the release, the page text is already live. Confirm at `https://no42-org.github.io/packyard/`.

## 4. Bump to next development version

After the release is tagged, bump `main` to the next version with a `-rc` suffix so builds on `main` are never mistaken for a release:

```bash
git checkout -b chore/bump-1.2.4-rc

sed -i "s/const version = .*/const version = \"1.2.4-rc\"/" auth/cmd/server/version.go
echo "1.2.4-rc" > rpm/VERSION
echo "1.2.4-rc" > static/VERSION

sed -i "s|packyard-auth:[^ ]*|packyard-auth:1.2.4-rc|" compose.yml
sed -i "s|packyard-rpm:[^ ]*|packyard-rpm:1.2.4-rc|" compose.yml
sed -i "s|packyard-static:[^ ]*|packyard-static:1.2.4-rc|" compose.yml

git add auth/cmd/server/version.go rpm/VERSION compose.yml
git commit -m "chore: bump versions to 1.2.4-rc post v1.2.3 release"
git push -u origin chore/bump-1.2.4-rc
gh pr create --title "chore: bump versions to 1.2.4-rc post v1.2.3" --fill
```

Builds on `main` will tag images with the `-rc` version until the next release cycle begins.
