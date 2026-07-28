# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| 1.x     | Yes       |
| < 1.0   | No        |

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security problems.

Report privately by emailing the maintainers at **Zennitex** (repository owner: [leonardo-turque](https://github.com/leonardo-turque)) or via GitHub Security Advisories on this repository:

https://github.com/leonardo-turque/Clicars-Search/security/advisories/new

Include:

- Description of the issue
- Steps to reproduce
- Affected component (API, scraper, frontend, WhatsApp, Docker)
- Potential impact

We aim to acknowledge reports within a few business days and coordinate a fix and disclosure timeline.

## Secrets

Never commit `.env`, API keys, or WhatsApp admin credentials. Use `.env.example` as a template only.
