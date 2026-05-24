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

All records are on the `example.org` zone. Apply these **before** starting the deployment — Traefik's ACME TLS-ALPN-01 challenge requires the hostnames to resolve to the VM before it can issue the TLS certificate.

| Type  | Name                       | Value                              | TTL   | Notes |
|-------|----------------------------|------------------------------------|-------|-------|
| A     | `pkg.example.org`          | `<VM_IPV4>`                        | 300   | Primary package serving endpoint |
| AAAA  | `pkg.example.org`          | `<VM_IPV6>`                        | 300   | Only if VM has a public IPv6 address |
| A     | `admin.pkg.example.org`    | `<VM_IPV4>`                        | 300   | Admin UI + admin API (OAuth-gated) |
| AAAA  | `admin.pkg.example.org`    | `<VM_IPV6>`                        | 300   | Only if VM has a public IPv6 address |
| CAA   | `pkg.example.org`          | `0 issue "letsencrypt.org"`        | 3600  | Restricts TLS cert issuance to Let's Encrypt |
| CAA   | `pkg.example.org`          | `0 iodef "mailto:ops@example.org"` | 3600  | CAA violation notification address |

> **Admin host:** `admin.pkg.example.org` is gated by OAuth + operator allowlist (see [Operator onboarding](operator-onboarding.md)). The historical loopback admin entrypoint (`127.0.0.1:8088`) has been retired — the admin API is now exposed over public TLS but requires a valid operator session.

### DNS propagation check

```bash
dig +short pkg.example.org A
dig +short admin.pkg.example.org A
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
| operator CIDR | TCP 22 | SSH access to the VM (host-level ops; admin API is HTTPS-only) |

> Traefik uses the **TLS-ALPN-01** challenge for Let's Encrypt — port 443 is the only port required. Port 80 does not need to be open.

---

## 3. Secrets & Environment

### `.env` file (on VM, never committed)

```dotenv
# TLS + hostnames
ACME_EMAIL=ops@example.org
PKG_DOMAIN=pkg.example.org
ADMIN_DOMAIN=admin.pkg.example.org

# Admin bootstrap — the first operator inserted when the operators table is
# empty. Idempotent: ignored once any operator exists. See the operator
# onboarding guide for the full flow.
PACKYARD_BOOTSTRAP_OPERATOR_EMAIL=ops@example.org

# OAuth providers — configure at least one; configure both for redundancy.
# Every variable in a provider's group must be set together; partial config
# fails the auth service at startup.

# GitHub
PACKYARD_GITHUB_CLIENT_ID=<from GitHub OAuth App>
PACKYARD_GITHUB_CLIENT_SECRET=<from GitHub OAuth App>
PACKYARD_GITHUB_REDIRECT_URI=https://admin.pkg.example.org/api/v1/auth/callback/github
PACKYARD_GITHUB_ORG=<github-org-login>

# Microsoft Entra (Azure AD)
PACKYARD_MICROSOFT_CLIENT_ID=<from Entra app registration>
PACKYARD_MICROSOFT_CLIENT_SECRET=<from Entra app registration>
PACKYARD_MICROSOFT_REDIRECT_URI=https://admin.pkg.example.org/api/v1/auth/callback/microsoft
PACKYARD_MICROSOFT_TENANT_ID=<from Entra directory>

# RustFS staging storage (generate with: openssl rand -hex 20)
RUSTFS_ACCESS_KEY=<generate>
RUSTFS_SECRET_KEY=<generate>
```

See [Operator onboarding §1](operator-onboarding.md#1-register-the-idp-apps-one-time-per-deployment)
for how to register the GitHub and Microsoft applications and obtain the
client id / secret / tenant id values.

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

Replace `core` and `minion` in the examples below with component names provisioned via `POST /api/v1/components`.

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

# 5. Admin host reachable, but unauthenticated /api/v1 returns 401
curl -sI https://admin.pkg.example.org/api/v1/operators
# Expect: HTTP/2 401 with `code: "UNAUTHORIZED"` body

# 6. Admin login page renders
curl -sI https://admin.pkg.example.org/admin/login
# Expect: HTTP/2 200 with text/html
```

---

## 9. First Subscriber Onboarding

The bootstrap operator (see `.env → PACKYARD_BOOTSTRAP_OPERATOR_EMAIL`) signs
in at `https://admin.pkg.example.org/admin/login` using their IdP, then
uses the SPA to create a subscriber account and issue its first key.

For headless / scripted use, the same flow goes through the API. Capture
the session cookie from a browser login (DevTools → Application →
Cookies → `packyard_session`) and pass it via `curl --cookie`:

```bash
COOKIE="packyard_session=…"

# 1. Create a subscriber account
ACCOUNT=$(curl -s -X POST https://admin.pkg.example.org/api/v1/accounts \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"email":"ops@acme.test","org_name":"Acme Corp"}' | jq -r .id)

# 2. Issue a subscription key for the account
curl -s -X POST "https://admin.pkg.example.org/api/v1/accounts/${ACCOUNT}/keys" \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"component":"core","label":"Acme — Core"}' | jq .
# The `id` field IS the subscription key — there is no separate `secret`
# field. Share `id` with the subscriber verbatim; rotation requires
# revoking this key and issuing a fresh one. See docs/reference/api.md
# § Subscription keys for the full schema.
```

See [Operator onboarding](operator-onboarding.md) for how to allowlist
additional admins or auditor-style readonly operators.

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

Prometheus metrics are available at `http://auth:9090/metrics` (internal Docker network only). Traefik's own metrics are at `http://traefik:8082/metrics` on the internal `metrics` entrypoint. Expose either to an internal monitoring stack by adding a Prometheus container on the `proxy` Docker network, or scrape over SSH if a stack is hosted elsewhere.
