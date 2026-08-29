#!/usr/bin/env bash
set -euo pipefail
REPO="leonardo-turque/Clicars-Search"

protect() {
  local branch="$1"
  gh api -X PUT "repos/${REPO}/branches/${branch}/protection" \
    --input - <<EOF
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Backend (Go)", "Frontend (Next.js)"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "required_approving_review_count": 0,
    "dismiss_stale_reviews": true
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "block_creations": false,
  "required_conversation_resolution": true,
  "lock_branch": false,
  "allow_fork_syncing": false
}
EOF
  echo "Protected: ${branch}"
}

protect main
protect develop

# Default PR base = develop
gh repo edit "$REPO" --default-branch main \
  --description "Busca de empresas por nicho e localização (Google Maps + Go + Next.js)" \
  --add-topic search --add-topic google-maps --add-topic golang --add-topic nextjs --add-topic whatsapp --add-topic docker

# Create release from tag
gh release create v1.0.0 --repo "$REPO" --title "v1.0.0 — Initial stable release" --notes-file CHANGELOG.md 2>/dev/null || \
  gh release edit v1.0.0 --repo "$REPO" --title "v1.0.0 — Initial stable release" --notes-file CHANGELOG.md

# Open PRs
gh pr create --repo "$REPO" --base develop --head feature/whatsapp-api-zennitex \
  --title "feat(whatsapp): integrate hosted Zennitex WhatsApp API client" \
  --body "$(cat <<'EOF'
## Summary
- Replaces embedded whatsmeow sessions with the remote Zennitex WhatsApp API client
- Updates env, Compose, and docs for `WHATSAPP_API_URL` / `WHATSAPP_ADMIN_KEY`

## Test plan
- [ ] `cd backend && go test ./...`
- [ ] Connect a number via QR and list sessions
- [ ] Smoke-test a campaign send with a test instance

EOF
)" || true

echo "Done."
