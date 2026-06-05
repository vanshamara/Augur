# Security

## Supported Versions

Augur is pre-1.0. Security fixes are applied to the main branch.

## Reporting a Vulnerability

Please do not open a public issue for a security problem.

Use GitHub Security Advisories for this repository when available. If that is
not available, contact the maintainer privately before posting details.

Include:

- A short summary of the issue.
- Steps to reproduce it.
- The affected config or feature.
- Any known impact.

Do not include real API keys, tenant keys, customer prompts, traces, or private
logs. Redact secrets before sharing examples.

## Scope

In scope:

- Auth bypasses.
- Secret leaks.
- Unsafe defaults.
- Denial of service paths.
- Routing isolation bugs.
- Provider key handling bugs.

Out of scope:

- Vulnerabilities that require access to local ignored files such as `.env`.
- Reports without a clear security impact.
- Issues caused only by exposing Augur without gateway auth in an untrusted
  network.
