# Subscriber Usage

Subscribers authenticate with HTTP Basic auth: username `subscriber`, password = subscription key.

All examples below use the component name `core`. Replace `core` with the component your subscription key is scoped to — check with your Packyard administrator if unsure.

## RPM (dnf/yum)

```ini
# /etc/yum.repos.d/lts.repo
[lts-core]
name=LTS Core
baseurl=https://subscriber:KEY@pkg.example.org/rpm/core/2025/el9-x86_64/
enabled=1
gpgcheck=1
gpgkey=https://pkg.example.org/gpg/lts.asc
```

## DEB (apt)

```bash
# Download the GPG key
curl -fsSL https://pkg.example.org/gpg/lts.asc \
  | gpg --dearmor > /usr/share/keyrings/lts.gpg

# /etc/apt/sources.list.d/lts.list
deb [signed-by=/usr/share/keyrings/lts.gpg] \
  https://subscriber:KEY@pkg.example.org/deb/core/2025/ bookworm main
```

## OCI (Docker / Kubernetes)

```bash
# Authenticate
docker login pkg.example.org/oci \
  --username subscriber \
  --password KEY

# Pull
docker pull pkg.example.org/oci/lts-core:2025

# Verify the signature. Images are signed keylessly by the promote-oci
# workflow, so you pin the signing workflow's identity rather than a key.
# cosign consults the Sigstore transparency log, which needs outbound HTTPS.
cosign verify \
  --certificate-identity-regexp 'https://github.com/no42-org/packyard/\.github/workflows/promote-oci\.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  pkg.example.org/oci/lts-core:2025
```

The signature is stored in the registry next to the image, so `docker pull` and `cosign verify` both go through the same authenticated `/oci/` endpoint.

## Public Keys

Signing keys are available without authentication:

| URL | Purpose |
|-----|---------|
| `https://pkg.example.org/gpg/lts.asc` | GPG public key for RPM/DEB verification |

OCI images have no distributed public key. They are verified against the signing workflow's identity as shown above.
