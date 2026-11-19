# Security Policy

## Supported versions

Security fixes are made on the `main` branch and included in the next release.
Only the latest published release is supported; older releases do not receive
backported fixes.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Use GitHub's private vulnerability reporting:

1. Open <https://github.com/xyz0z0/flowdrop/security/advisories/new>
   (or the repository's **Security** tab → **Report a vulnerability**).
2. Describe the issue, the affected component (Hub server, Web UI, desktop app,
   Android app), the version or commit, and the impact you observed.
3. Include reproduction steps or a proof of concept if you have one. If the
   report involves a paired device token, pairing code, cookie or any other
   secret, redact it.

If private reporting is unavailable to you, contact the maintainer through the
GitHub profile linked from the repository and ask for a private channel first;
do not post details in a public issue.

## What to expect

This is a personal, self-hosted project maintained on a best-effort basis.
Reports are acknowledged as soon as practical, and you will be told whether the
report is accepted, needs more information, or is out of scope. Please allow
time for a fix before public disclosure; coordinated disclosure is appreciated.

## Scope

In scope:

- Authentication, pairing, device-token and session handling.
- Web/HTTP API behaviour: CSRF and Origin/Host checks, path traversal, request
  smuggling, injection, unsafe file handling, attachment storage.
- Android app: intent handling, WebView usage, credential storage, exported
  components.
- Desktop/web UI: proxying, local file exposure, update paths.

Out of scope:

- Findings that require the attacker to already control the Hub host, the
  device, or the repository.
- Missing hardening headers or TLS configuration on deployments where the
  operator deliberately uses plain HTTP on a trusted LAN (documented in
  `docs/DEPLOY.md`).
- Issues in third-party dependencies without a FlowDrop-specific impact; report
  those upstream (see `THIRD_PARTY_NOTICES.md` for the dependency inventory).
- Denial of service through resource exhaustion on a Hub you do not own.

## Testing guidelines

Only test against a Hub instance you own or have explicit permission to test.
Do not access, modify or exfiltrate other people's data, and do not run
automated scanning against third-party deployments.
