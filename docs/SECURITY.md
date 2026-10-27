# Security

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

## Security model

### Sign-in

- Passwords are hashed with argon2id, and only a few hashes run at once so a burst of sign-ins can't exhaust memory.
- TOTP two-factor sign-in rejects reused codes.
- Each sign-in gets 5 wrong codes before the password is needed again, and codes are also limited per account and per address.

### Sessions

- Cookies are HttpOnly and SameSite=Strict, and Secure over HTTPS.
- State-changing requests must be JSON from the same origin.
- A strict Content Security Policy is sent with every page.
- Signing out, changing the password or turning on 2FA ends the other sessions, including their live event streams.

### Secrets

- Channel tokens, connection strings, monitor headers and 2FA secrets are encrypted at rest with AES-256-GCM.
- They are never sent back to the browser.
- A stored secret stays tied to its destination: if you change where it goes, you have to type it in again.

### Tokens

Session, agent, kiosk and push tokens are 256-bit random values. The hub stores only their SHA-256 hashes.

### Agents

- An agent token only affects its own server: frame budgets, sanitised metrics and alert budgets keep one compromised server from flooding the hub.
- A server acts as a probe location only when the admin enables it.
- Probes go only to public addresses, are checked at connect time, and never carry headers or credentials.

### Checks

- A panic in one check can't crash the hub.
- Database passwords go only to servers with a verified certificate, unless you opt out per monitor.
- Hub checks of a public target can't be redirected into the hub's private network.

### Public pages

Status pages and kiosk boards return names and states only: no targets, URLs, hosts, IPs or error messages.

## Audit

The codebase had a full security audit on 2026-10-27. It found 23 issues: 7 medium and 16 low, none high or critical. All 23 were fixed with regression tests in commit `96d1ae9`.

Four further leads still need checks in a real browser or deployment and remain open:

- DNS rebinding during first-time setup.
- Slack mention markup in alert text.
- The agent install command behind a TLS proxy.
- Memory use against hostile database servers.

## Reporting a vulnerability

Please report security issues privately to the maintainer through GitHub rather than in a public issue.
