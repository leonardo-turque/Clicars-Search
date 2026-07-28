# Contributing to Clicars Search

Thanks for helping improve Clicars Search. This document describes how we work on GitHub so `main` stays stable and every change is reviewable.

## Branch model

| Branch | Role |
|--------|------|
| `main` | Production-ready code only. Protected — **no direct commits or pushes**. |
| `develop` | Integration branch for the next release. |
| `feature/*` | New features (e.g. `feature/whatsapp-api-zennitex`) |
| `fix/*` | Bug fixes |
| `chore/*` | Tooling, docs, CI, repo hygiene |
| `docs/*` | Documentation-only changes |
| `release/*` | Release preparation (changelog, version bumps) |

### Flow

```text
feature/* ──PR──► develop ──PR──► main ──tag──► vX.Y.Z
```

1. Create a branch from `develop` (or from `main` for urgent hotfixes).
2. Open a Pull Request into `develop`.
3. After review and CI green, merge with squash or merge commit (no force-push to shared branches).
4. When ready to release, open a PR `develop` → `main`, then tag `vX.Y.Z` and update `CHANGELOG.md`.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(search): support quantity up to 5000
fix(whatsapp): reconnect sessions after API restart
docs(readme): fix clone URL
chore(ci): add Go and frontend test workflow
```

Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `perf`.

## Pull requests

- Fill in the PR template (summary + test plan).
- Keep PRs focused — one concern per PR when practical.
- Ensure tests pass locally before requesting review:

  ```bash
  # Backend
  cd backend && go test ./...

  # Frontend
  cd frontend && npm test
  ```

## Local guard: do not push to `main`

This repo ships a `pre-push` hook that **blocks pushes to `main`**. Enable it once after cloning:

```bash
git config core.hooksPath .githooks
```

Branch protection on GitHub is the source of truth; the hook is a local safety net.

## Versioning

We use [SemVer](https://semver.org/):

- **MAJOR** — breaking API or behavior changes
- **MINOR** — backward-compatible features
- **PATCH** — backward-compatible bug fixes

Release tags look like `v1.0.0`. See [CHANGELOG.md](./CHANGELOG.md).

## Security

Do not open public issues for vulnerabilities. See [SECURITY.md](./SECURITY.md).
