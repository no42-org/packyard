// Content tables for the Packyard landing page.
// Kept as a plain module so it's cheap to tweak without touching components.

export type Feature = { icon: string; title: string; body: string };
export type DocGroup = { group: string; body: string; links: { t: string; h: string }[] };
export type TerminalLine =
  | { k: 'cmd' | 'out' | 'ok'; text: string }
  | { k: 'bar'; text: string }
  | { k: 'blank' };

export const FEATURES: Feature[] = [
  { icon: 'pkg',    title: 'RPM · DEB · OCI',       body: 'One server, three repository formats. dnf, apt, and docker pull all hit the same Traefik front door with subscription-key auth.' },
  { icon: 'auth',   title: 'Forward-auth gating',   body: 'Per-subscriber keys scoped per-component. Traefik forwardAuth middleware calls the Go auth service before every request — no client cert dance.' },
  { icon: 'sign',   title: 'Signed artefacts',      body: 'GPG signs RPM and DEB indices; cosign signs OCI manifests keylessly. The GPG key is served unauthenticated at /gpg; images verify against the signing workflow identity.' },
  { icon: 'promote',title: 'Promotion pipeline',    body: 'GitHub Actions stage artefacts to RustFS (S3-compatible), sign them, then publish to the rpm / deb / oci backends in one workflow.' },
  { icon: 'metric', title: 'Observability',         body: 'Prometheus metrics on the auth service, structured admin API with Code + Message error responses, daily SQLite backup of the key store.' },
  { icon: 'self',   title: 'Self-hosted',           body: 'docker compose v2 stack — Traefik, auth, nginx, Zot, Aptly, RustFS. No SaaS dependency, no per-subscriber licensing meter.' },
];

// Paths match sidebars.ts / docs folder exactly.
export const DOCS: DocGroup[] = [
  { group: 'Getting Started', body: 'Stand up the stack locally and run your first authenticated request.', links: [
    { t: 'Quick start', h: '/getting-started/quick-start' },
  ]},
  { group: 'Operations', body: 'Deploy in production, promote releases, restore the keystore, plan manual tests.', links: [
    { t: 'Production deployment', h: '/ops/production-deployment' },
    { t: 'Package promotion',     h: '/ops/release-runbook' },
    { t: 'Restore keystore',      h: '/ops/restore-keystore' },
    { t: 'Manual test plan',      h: '/ops/manual-test-plan' },
    { t: 'Troubleshooting',       h: '/ops/troubleshooting' },
  ]},
  { group: 'Reference', body: 'Architecture, admin API, configuration, subscriber integration, promotion pipeline.', links: [
    { t: 'Architecture',      h: '/reference/architecture' },
    { t: 'Subscriber usage',  h: '/reference/subscriber-usage' },
    { t: 'Admin API',         h: '/reference/api' },
    { t: 'Promotion pipeline',h: '/reference/promotion-pipeline' },
    { t: 'Observability',     h: '/reference/observability' },
    { t: 'Configuration',     h: '/reference/configuration' },
  ]},
];

export const TERMINAL_SCRIPT: TerminalLine[] = [
  { k: 'cmd', text: 'sudo dnf install -y lts-bundle' },
  { k: 'out', text: 'Last metadata expiration check: 0:00:01 ago on Mon 11 May 2026.' },
  { k: 'out', text: 'Dependencies resolved.' },
  { k: 'out', text: 'Package          Version            Repository          Size' },
  { k: 'out', text: 'lts-bundle       2025.1-1.el9       lts-core            6.4 M' },
  { k: 'ok',  text: 'Installed: lts-bundle-2025.1-1.el9' },
  { k: 'blank' },
  { k: 'cmd', text: 'sudo apt-get install -y lts-bundle' },
  { k: 'out', text: 'Reading package lists... Done' },
  { k: 'out', text: 'Building dependency tree... Done' },
  { k: 'out', text: 'Get:1 https://pkg.example.org/deb/core/2025 bookworm/main amd64 lts-bundle 2025.1 [4.7 MB]' },
  { k: 'ok',  text: 'Setting up lts-bundle (2025.1) ...' },
  { k: 'blank' },
  { k: 'cmd', text: 'docker pull pkg.example.org/oci/lts-core:2025' },
  { k: 'out', text: '2025: Pulling from oci/lts-core' },
  { k: 'out', text: 'Digest: sha256:9f3b4a8c2e1d…' },
  { k: 'ok',  text: 'Verified by cosign · 3 layers · 41 MB' },
];
