# Production Deployment — pkg.example.org

**Target domain:** `example.org` | **Primary hostname:** `pkg.example.org` | **Last updated:** 2026-04-13

---

## Table of Contents

1. [DNS Records](#1-dns-records)
2. [VM Requirements](#2-vm-requirements)
3. [Secrets & Environment](#3-secrets--environment)
4. [Generating Signing Keys](#4-generating-signing-keys)
5. [Creating the deploy User](#5-creating-the-deploy-user)
6. [Pre-Deployment Checklist](#6-pre-deployment-checklist)
7. [Deployment Steps](#7-deployment-steps)
8. [Post-Deployment Validation](#8-post-deployment-validation)
9. [First Subscriber Onboarding](#9-first-subscriber-onboarding)
10. [Run the Verification Suite](#10-run-the-verification-suite)
11. [Monitoring](#11-monitoring)

---

## 1. DNS Records

All records are on the `example.org` zone. Apply these **before** starting the deployment — Traefik's ACME TLS-ALPN-01 challenge requires `pkg.example.org` to resolve to the VM before it can issue the TLS certificate.

| Type  | Name              | Value                            | TTL   | Notes |
|-------|-------------------|----------------------------------|-------|-------|
| A     | `pkg.example.org`     | `<VM_IPV4>`                      | 300   | Primary package serving endpoint |
| AAAA  | `pkg.example.org`     | `<VM_IPV6>`                      | 300   | Only if VM has a public IPv6 address |
| CAA   | `pkg.example.org`     | `0 issue "letsencrypt.org"`      | 3600  | Restricts TLS cert issuance to Let's Encrypt |
| CAA   | `pkg.example.org`     | `0 iodef "mailto:ops@example.org"`   | 3600  | CAA violation notification address |

> **No other subdomains are needed.** The admin API is loopback-only (`127.0.0.1:8088`), reached via SSH tunnel. RPM, DEB, OCI, and GPG key endpoints all share `pkg.example.org`.

### DNS propagation check

```bash
dig +short pkg.example.org A
dig +short pkg.example.org CAA
```

---

## 2. VM Requirements

| Resource   | Minimum          | Notes |
|------------|------------------|-------|
| OS         | Ubuntu 24.04 LTS | Or any Docker-compatible Linux |
| CPU        | 2 vCPU           | Auth service + nginx are lightweight |
| RAM        | 4 GB             | Aptly snapshot creation peaks at ~1.5 GB |
| Disk       | 100 GB           | RPM/DEB/OCI artifact storage; size to expected package volume |
| Ports open | TCP 22, TCP 443  | 22 for SSH/operator access; 443 for package serving |
| Docker     | 26+              | With Compose plugin v2 |

**Firewall rules:**

| Source | Protocol/Port | Purpose |
|--------|---------------|---------|
| `0.0.0.0/0` | TCP 443 | Package subscribers + TLS-ALPN-01 cert issuance |
| operator CIDR | TCP 22 | SSH for admin API access and port-forwards |

> Traefik uses the **TLS-ALPN-01** challenge for Let's Encrypt — port 443 is the only port required. Port 80 does not need to be open.

---

## 3. Secrets & Environment

### `.env` file (on VM, never committed)

```dotenv
# TLS
ACME_EMAIL=ops@example.org
PKG_DOMAIN=pkg.example.org

# RustFS staging storage (generate with: openssl rand -hex 20)
RUSTFS_ACCESS_KEY=<generate>
RUSTFS_SECRET_KEY=<generate>
```

### GitHub Actions secrets

| Secret name           | Value source                                   |
|-----------------------|------------------------------------------------|
| `HOST`                | `pkg.example.org`                                  |
| `SSH_PRIVATE_KEY`     | Private key for the `deploy` user on VM        |
| `SSH_KNOWN_HOST`      | `ssh-keyscan pkg.example.org` output               |
| `RUSTFS_ACCESS_KEY`   | Same as `.env`                                 |
| `RUSTFS_SECRET_KEY`   | Same as `.env`                                 |
| `GPG_PRIVATE_KEY`     | ASCII-armored LTS GPG signing key         |
| `GPG_KEY_ID`          | Key fingerprint (40 hex chars, no spaces)      |
| `GPG_PASSPHRASE`      | GPG key passphrase                             |
| `COSIGN_PRIVATE_KEY`  | Contents of `cosign.key`                       |
| `COSIGN_PASSWORD`     | cosign key passphrase                          |

---

## 4. Generating Signing Keys

Keys are generated once, kept offline in a secrets manager (e.g. 1Password, Vault), and loaded into GitHub Actions secrets. **Never commit private keys to the repository.**

### 4.1 GPG signing key

Used to sign RPM and DEB packages at promotion time. Use a dedicated key for LTS — do not reuse an operator's personal key.

```bash
# 1. Generate the key (batch mode, no TTY required)
cat > /tmp/lts-gpg-params <<'EOF'
%echo Generating LTS signing key
Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: LTS
Name-Comment: Package Signing Key
Name-Email: lts-signing@example.org
Expire-Date: 0
Passphrase: <CHOOSE_A_STRONG_PASSPHRASE>
%commit
%echo Done
EOF

gpg --batch --gen-key /tmp/lts-gpg-params
rm /tmp/lts-gpg-params

# 2. Find the 40-character fingerprint — this is GPG_KEY_ID
gpg --list-keys --fingerprint lts-signing@example.org
GPG_KEY_ID="<40-char fingerprint, no spaces>"

# 3. Export ASCII-armored private key — this is GPG_PRIVATE_KEY
gpg --armor --export-secret-keys "$GPG_KEY_ID"

# 4. Export public key and commit it to the repo
gpg --armor --export "$GPG_KEY_ID" > static/content/gpg/lts.asc
# git add static/content/gpg/lts.asc && git commit
```

**Secrets to set:**

| Secret | Value |
|--------|-------|
| `GPG_PRIVATE_KEY` | Output of `gpg --armor --export-secret-keys "$GPG_KEY_ID"` |
| `GPG_KEY_ID` | 40-character fingerprint (no spaces) |
| `GPG_PASSPHRASE` | Passphrase chosen during key generation |

### 4.2 cosign key pair

Used to sign OCI container images at promotion time (offline key-based signing, no Sigstore/Rekor).

```bash
# 1. Generate the key pair (cosign prompts for a password → COSIGN_PASSWORD)
cosign generate-key-pair
# cosign.key  — encrypted private key  → COSIGN_PRIVATE_KEY secret
# cosign.pub  — public key             → committed to repo

# 2. Commit the public key
cp cosign.pub static/content/gpg/cosign.pub
# git add static/content/gpg/cosign.pub && git commit

# 3. Copy private key contents into the COSIGN_PRIVATE_KEY secret, then shred local file
cat cosign.key
shred -u cosign.key
```

**Secrets to set:**

| Secret | Value |
|--------|-------|
| `COSIGN_PRIVATE_KEY` | Contents of `cosign.key` |
| `COSIGN_PASSWORD` | Password entered during `cosign generate-key-pair` |

### 4.3 Key storage checklist

- [ ] GPG private key exported and stored in secrets manager
- [ ] GPG key ID (fingerprint) noted
- [ ] GPG passphrase stored in secrets manager
- [ ] cosign private key stored in secrets manager, local copy shredded
- [ ] cosign password stored in secrets manager
- [ ] `static/content/gpg/lts.asc` committed to repository
- [ ] `static/content/gpg/cosign.pub` committed to repository
- [ ] All 10 secrets set in GitHub Actions repository settings

---

## 5. Creating the `deploy` User

Run on the VM as `root` or via `sudo`.

```bash
# Create user with login shell (required for git clone and docker compose)
useradd --create-home --shell /bin/bash deploy
usermod -aG docker deploy
```

Generate an SSH key pair on your **local machine**:

```bash
ssh-keygen -t ed25519 -C "packyard-deploy" -f ~/.ssh/packyard_deploy
# ~/.ssh/packyard_deploy      — private key (keep secret)
# ~/.ssh/packyard_deploy.pub  — public key  (goes on the VM)
```

Authorize the public key on the VM:

```bash
# On the VM as root
mkdir -p /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
echo "<paste ~/.ssh/packyard_deploy.pub>" >> /home/deploy/.ssh/authorized_keys
chmod 600 /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh
```

Verify access, then capture secrets:

```bash
# Test login
ssh -i ~/.ssh/packyard_deploy deploy@pkg.example.org

# SSH_PRIVATE_KEY secret value
cat ~/.ssh/packyard_deploy

# SSH_KNOWN_HOST secret value (run after DNS propagates)
ssh-keyscan pkg.example.org
```

---

## 6. Pre-Deployment Checklist

- [ ] DNS A record for `pkg.example.org` propagated (`dig` confirms VM IP)
- [ ] DNS CAA record for `pkg.example.org` present
- [ ] VM firewall: `tcp/443` open to internet
- [ ] Docker + Compose plugin v2 installed on VM
- [ ] `deploy` user created, added to `docker` group, SSH key authorized (§5)
- [ ] GPG LTS signing key generated (§4.1); `lts.asc` committed to `static/content/gpg/`
- [ ] cosign key pair generated (§4.2); `cosign.pub` committed to `static/content/gpg/`
- [ ] `.env` file written on VM with production values (§3)
- [ ] All 10 GitHub Actions secrets set in repository settings (§3)

---

## 7. Deployment Steps

```bash
# On the VM as the deploy user
git clone <packyard-repo> ~/packyard
cd ~/packyard

# Write .env (see §3)
# Ensure static/content/gpg/lts.asc and cosign.pub are present

docker compose pull
docker compose up -d

# Watch for Traefik to obtain the Let's Encrypt certificate (up to 2 min)
docker compose logs traefik -f
```

Expected cert issuance log line:

```
traefik  | msg="Certificate obtained successfully" domain=pkg.example.org
```

---

## 8. Post-Deployment Validation

Run these from an **external host**, not the VM itself.

Replace `core` and `minion` in the examples below with component names from your `config/packyard.yml`.

```bash
# 1. GPG key endpoint — tests TLS + routing (unauthenticated)
curl -sI https://pkg.example.org/gpg/lts.asc
# Expect: HTTP/2 200, Content-Type: text/plain

# 2. Package endpoint rejects unauthenticated requests
curl -sI https://pkg.example.org/rpm/core/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 401

# 3. Valid key is accepted
curl -sI -u subscriber:<KEY> https://pkg.example.org/rpm/core/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 200 (after first promotion) or 404 if no artifacts yet

# 4. Wrong-component key is rejected
curl -sI -u subscriber:<CORE_KEY> https://pkg.example.org/rpm/minion/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 401

# 5. Admin API reachable only via SSH tunnel
ssh -L 8088:127.0.0.1:8088 deploy@pkg.example.org -N &
curl -s http://127.0.0.1:8088/api/v1/keys
# Expect: JSON array (empty if no keys created yet)
```

---

## 9. First Subscriber Onboarding

```bash
# Open SSH tunnel to admin API
ssh -L 8088:127.0.0.1:8088 deploy@pkg.example.org -N &

# Create an API key for a subscriber
# Replace "core" with a component name from config/packyard.yml
curl -s -X POST http://127.0.0.1:8088/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "Acme Corp — Core"}'
# Response contains the key value — share only this with the subscriber
```

Example subscriber `yum.repos.d` entry:

```ini
[onms-lts-core]
name=LTS Core
baseurl=https://subscriber:<KEY>@pkg.example.org/rpm/core/2025/el9-x86_64/
enabled=1
gpgcheck=1
gpgkey=https://pkg.example.org/gpg/lts.asc
```

---

## 10. Run the Verification Suite

Two scripts verify the stack end-to-end. Run them in order.

### 10.1 Container health check

Run on the VM as the `deploy` user. Checks container states, GPG endpoint, auth service reachability, admin API isolation, RPM routing, network isolation, and RustFS health:

```bash
cd ~/packyard
PKG_DOMAIN=pkg.example.org bash scripts/health-check.sh
```

Expected: all lines start with `OK:` and the script exits `All services healthy`.

### 10.2 Remote smoke test

Run from any machine with network access to the deployment. Requires the subscriber key created in [§9](#9-first-subscriber-onboarding):

```bash
# Clone the repo locally if needed
git clone <packyard-repo> packyard && cd packyard

bash verify.sh \
  --base-url https://pkg.example.org \
  --test-key "$KEY" \
  --test-component core
```

Expected output ends with:
```
=== Results: N passed, 0 failed ===
```

The remote mode covers: public GPG endpoints, forwardAuth allow/deny, scope enforcement, and OCI scope — without touching the admin API or the Docker socket.

---

## 11. Monitoring

| Check | Method | SLA |
|-------|--------|-----|
| Endpoint availability | HTTP GET `https://pkg.example.org/gpg/lts.asc` from external monitor | 99.9% monthly |
| TLS cert expiry | Alert at ≤ 30 days remaining | — |
| Auth service health | Traefik health check (auto; returns 503 on failure) | Fail-closed |

Prometheus metrics are available at `http://auth:9090/metrics` (internal Docker network only). Expose to an internal monitoring stack via SSH tunnel or a separate Traefik route on the admin entrypoint.
