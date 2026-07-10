# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
pull requests, or discussions.**

Instead, report them privately using GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability):

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Fill in the advisory form with as much detail as you can.

If you cannot use GitHub's tooling, email the maintainers at
**security@opensynccrdt.dev** with the details.

Please include, where possible:

- A description of the vulnerability and its impact.
- The affected version(s) or commit(s).
- Step-by-step reproduction instructions or a proof of concept.
- Any relevant configuration (auth mode, storage backend, cluster mode).

## Response commitment

- **Acknowledgement:** within **3 business days** of your report.
- **Initial assessment** (severity and whether we can reproduce it): within
  **7 business days**.
- **Fix and disclosure timeline:** we aim to ship a fix and publish a
  coordinated advisory within **90 days** of triage, and sooner for
  actively-exploited or critical issues. We will keep you updated on progress
  and credit you in the advisory unless you ask us not to.

Please give us reasonable time to address the issue before any public
disclosure.

## Supported versions

Security fixes are released for the latest minor version. Older minor versions
are not maintained; upgrade to the latest release to receive fixes.

| Version   | Supported          |
| --------- | ------------------ |
| Latest `0.x` minor | :white_check_mark: |
| Older `0.x` minors | :x:                |

Because OpenSyncCRDT is pre-1.0, minor versions may include breaking changes;
see [CHANGELOG.md](CHANGELOG.md) before upgrading.

## Scope notes

OpenSyncCRDT handles sync infrastructure only. By design it does **not**
implement user management, token issuance/validation, or per-document access
control — those are the developer's responsibility (see
[docs/auth.md](docs/auth.md)). Reports about the absence of these features are
out of scope. Reports about flaws in the features we *do* provide — the auth
modes, HMAC webhook signing, the management API key, TLS handling, or the
storage/cluster layers — are in scope and very welcome.
