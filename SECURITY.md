# Security Policy

VAC handles secrets (env vars, SSH deploy keys, TOTP secrets, webhook URLs) and runs untrusted
Compose stacks on a single host. Security reports are taken seriously.

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately via one of:

- **GitHub Security Advisories** — use the [**Report a vulnerability**](../../security/advisories/new) button on this repository (preferred).
- **Email** — `vojir.mikulas@gmail.com` with a subject starting `[VAC SECURITY]`.

Please include:

- a description of the issue and its impact,
- steps to reproduce (a proof of concept if possible),
- affected version / commit, and
- any suggested remediation.

## What to expect

- **Acknowledgement** within 72 hours.
- An initial assessment and severity triage shortly after.
- Coordinated disclosure: we'll work with you on a fix and a disclosure timeline, and credit you
  in the release notes if you'd like.

Please give us a reasonable window to ship a fix before any public disclosure.

## Supported versions

VAC is pre-1.0 and ships from a single line of development. Security fixes land on the latest
release; please upgrade to the latest version before reporting.

## Scope

In scope: the `vac-api` backend, the dashboard UI, the Caddy proxy config, the installer, and
the deploy pipeline. Out of scope: vulnerabilities in third-party dependencies (report those
upstream) and issues that require an already-compromised host or operator credentials.
