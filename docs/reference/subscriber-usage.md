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

# Verify signature offline (after downloading cosign.pub once)
curl -fsSL https://pkg.example.org/gpg/cosign.pub -o /etc/lts/cosign.pub
cosign verify \
  --key /etc/lts/cosign.pub \
  --insecure-ignore-tlog \
  pkg.example.org/oci/lts-core:2025
```

## Public Keys

Signing keys are available without authentication:

| URL | Purpose |
|-----|---------|
| `https://pkg.example.org/gpg/lts.asc` | GPG public key for RPM/DEB verification |
| `https://pkg.example.org/gpg/cosign.pub` | cosign public key for OCI image verification |
