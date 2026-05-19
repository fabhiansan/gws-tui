# Security Policy

## Supported Versions

Security fixes are handled on the latest tagged release and the `main` branch.

## Reporting a Vulnerability

Please do not open a public issue for vulnerabilities that could expose tokens,
private Workspace data, message contents, attachments, or local cache paths.

Report security issues privately by email to the repository owner, or through
GitHub Security Advisories if enabled for the repository.

Include:

- The affected version or commit.
- The operating system and terminal environment.
- Whether daemon mode was enabled.
- A minimal reproduction that avoids sharing real Workspace data.
- Any relevant logs with tokens, email addresses, message bodies, attachment
  URLs, and local usernames redacted.

## Sensitive Data

`gws-tui` may interact with OAuth credentials owned by the upstream Google
Workspace CLI, plus local cache, state, draft, image, and daemon log files. See
`docs/PRIVACY.md` for the default local paths and cleanup guidance.
