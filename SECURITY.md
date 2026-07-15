# Security Policy

## Supported versions

This project is pre-1.0. Security fixes are applied to the latest released
minor version only. Please upgrade to the newest release before reporting.

| Version | Supported |
| ------- | --------- |
| latest  | yes       |
| older   | no        |

## Reporting a vulnerability

Please report vulnerabilities privately — do not open a public issue.

Use GitHub's private vulnerability reporting: go to the repository's
**Security** tab and click **Report a vulnerability**
(<https://github.com/timkrebs/vault-cost/security/advisories/new>).

Include as much detail as you can:

- affected version or commit
- a description of the issue and its impact
- steps to reproduce or a proof of concept
- any suggested remediation

You can expect an acknowledgement within a few business days. Once the issue is
confirmed and a fix is prepared, a new release will be published and the
advisory disclosed with credit to the reporter unless anonymity is requested.

## Handling of secrets

This plugin reads Vault client-activity data and authenticates with short-TTL
Kubernetes-auth tokens. It never logs tokens and redacts credentials in its
startup log. If you find a code path that leaks a secret, treat it as a
vulnerability and report it privately.
