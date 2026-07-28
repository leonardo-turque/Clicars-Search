# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Planned

- Integration with the hosted Zennitex WhatsApp API (see `feature/whatsapp-api-zennitex`)

## [1.0.0] — 2026-07-28

Initial stable release of **Clicars Search**.

### Added

- Google Maps company search via headless Chromium ([rod](https://github.com/go-rod/rod)) — no API key required
- Go 1.25 backend (Clean Architecture) with PostgreSQL 16 persistence
- Next.js 14 dashboard for search results and WhatsApp session management
- Multi-device WhatsApp pairing via whatsmeow (up to 15 numbers)
- Campaign dispatcher with anti-ban delay and rate limiting
- 45-day data retention worker with graceful shutdown
- Docker Compose stack (`db` + `api` + `frontend`)
- Automated tests for backend (Go) and frontend (Jest + React Testing Library)

### Documentation

- Architecture diagram (Mermaid), environment reference, and HTTP API docs in the README
- MIT license

[Unreleased]: https://github.com/leonardo-turque/Clicars-Search/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/leonardo-turque/Clicars-Search/releases/tag/v1.0.0
