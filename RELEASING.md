# Releasing Packyard

This covers cutting a new version of the Packyard infrastructure itself — the auth service, RPM service, and static service images, plus the versioned docs.

## 1. Bump version numbers

Two files must match the intended release tag (without the `v` prefix):

```bash
# auth service
# edit the version constant in auth/cmd/server/version.go

# rpm image
echo "1.2.3" > rpm/VERSION
```

Commit and merge to `main` before tagging.

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

Publishing the release triggers `docs.yml`, which runs `mike deploy v1.2.3 latest` and pushes the versioned docs to `gh-pages`. Confirm at `https://no42-org.github.io/packyard/`.
