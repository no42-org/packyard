# Security Policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub:

https://github.com/no42-org/packyard/security/advisories/new

Do not open a public issue or pull request for a security problem.
Include the affected version, steps to reproduce, and the impact you observed.

You will get an acknowledgement within 5 working days.
We aim to publish a fix and an advisory within 90 days of a confirmed report, sooner for actively exploited issues.
We will credit you in the advisory unless you ask us not to.

## Supported versions

Only the latest minor release line receives security fixes.
Check the [releases page](https://github.com/no42-org/packyard/releases) for the current version.

## Scope

In scope: the auth service, the promotion workflows, the Compose stack and its Traefik configuration, and the published container images.
Out of scope: the upstream images Packyard runs unmodified (Traefik, nginx, Zot, Aptly, RustFS). Report those to their maintainers.
