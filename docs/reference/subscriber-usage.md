# Subscriber Usage

Subscribers authenticate with HTTP Basic auth: username `subscriber`, password = subscription key.

## RPM (dnf/yum)

```ini
# /etc/yum.repos.d/meridian.repo
[meridian-core]
name=Meridian Core
baseurl=https://subscriber:KEY@pkg.example.com/rpm/core/2025/el9-x86_64/
enabled=1
gpgcheck=1
gpgkey=https://pkg.example.com/gpg/meridian.asc
```

## DEB (apt)

```bash
# Download the GPG key
curl -fsSL https://pkg.example.com/gpg/meridian.asc \
  | gpg --dearmor > /usr/share/keyrings/meridian.gpg

# /etc/apt/sources.list.d/meridian.list
deb [signed-by=/usr/share/keyrings/meridian.gpg] \
  https://subscriber:KEY@pkg.example.com/deb/core/2025/ bookworm main
```

## OCI (Docker / Kubernetes)

```bash
# Authenticate
docker login pkg.example.com/oci \
  --username subscriber \
  --password KEY

# Pull
docker pull pkg.example.com/oci/meridian-core:2025

# Verify signature offline (after downloading cosign.pub once)
curl -fsSL https://pkg.example.com/gpg/cosign.pub -o /etc/meridian/cosign.pub
cosign verify \
  --key /etc/meridian/cosign.pub \
  --insecure-ignore-tlog \
  pkg.example.com/oci/meridian-core:2025
```

## Public Keys

Signing keys are available without authentication:

| URL | Purpose |
|-----|---------|
| `https://pkg.example.com/gpg/meridian.asc` | GPG public key for RPM/DEB verification |
| `https://pkg.example.com/gpg/cosign.pub` | cosign public key for OCI image verification |
