#!/usr/bin/env bash
# gen-changelog.sh — fill the changelog at a release cut, laconically.
#
# Usage: scripts/gen-changelog.sh <version> [<since-ref>]
#   e.g. scripts/gen-changelog.sh v0.11.0
#
# Writes two entries for <version>, derived from the commits since the last
# release (or <since-ref>) via the Claude Code CLI:
#   • CHANGELOG.md          — PUBLIC, laconic, user-facing. Shipped in the binary
#                             and shown on the board's "What's new" panel.
#   • CHANGELOG.internal.md — PRIVATE dev notes (the why, risky bits, follow-ups).
#                             Gitignored: a local-only file, never pushed.
#
# If `claude` is missing or errors, it falls back to the raw commit list so a
# release is NEVER blocked by the changelog step. Re-running for a version that
# is already present is a no-op (idempotent).
set -euo pipefail

VERSION="${1:?usage: gen-changelog.sh <version> [since-ref]}"
cd "$(git rev-parse --show-toplevel)"

SINCE="${2:-$(git describe --tags --abbrev=0 2>/dev/null || echo '')}"
if [ -n "$SINCE" ]; then RANGE="${SINCE}..HEAD"; else RANGE="HEAD"; fi

PUBLIC="CHANGELOG.md"
PRIVATE="CHANGELOG.internal.md"
DATE="$(date +%Y-%m-%d)"

# Idempotent: bail if this version is already recorded.
if [ -f "$PUBLIC" ] && grep -q "^## ${VERSION} " "$PUBLIC" 2>/dev/null; then
  echo "gen-changelog: ${VERSION} already in ${PUBLIC}; nothing to do"
  exit 0
fi

COMMITS="$(git log --no-merges --pretty=format:'- %s' ${RANGE} 2>/dev/null || true)"
if [ -z "$COMMITS" ]; then
  echo "gen-changelog: no commits in ${RANGE}; nothing to do"
  exit 0
fi

# Ask the Claude Code CLI to condense the commits into a public + private entry.
# Commit subjects here are prefixed "Ultraflow: " followed by a raw task title;
# the prompt tells the model to ignore the prefix and rewrite into clean prose.
llm_entry() {
  local prompt
  prompt="You are writing the changelog for release ${VERSION} of Ultraflow — a local board that runs AI coding agents in parallel over the user's own CLI subscriptions.

Below are the git commit subjects for this release. Many are prefixed 'Ultraflow: ' followed by a raw, messy task title — ignore that prefix and rewrite into clean, user-facing language.

Output EXACTLY this format and nothing else (no preamble, no closing remarks):

<<<PUBLIC>>>
- <laconic user-facing bullet>
- <...>
<<<PRIVATE>>>
- <candid internal note: the why, risky changes, follow-ups>
- <...>

Rules:
- 2-6 bullets per section. Terse, imperative, no fluff, no version header (it is added for you).
- Group related commits into one bullet. Drop pure chore/noise from PUBLIC.
- PUBLIC is what users read — features and fixes, plainly. PRIVATE can be blunt.

Commits:
${COMMITS}"

  # Cap the call so a hung/unauthenticated CLI (e.g. headless under launchd at a
  # release cut) can never block the release — we just fall back to raw commits.
  if command -v timeout >/dev/null 2>&1; then
    timeout 120 claude -p "$prompt" 2>/dev/null || true
  elif command -v gtimeout >/dev/null 2>&1; then
    gtimeout 120 claude -p "$prompt" 2>/dev/null || true
  else
    claude -p "$prompt" 2>/dev/null || true
  fi
}

RAW=""
if command -v claude >/dev/null 2>&1; then
  echo "gen-changelog: summarizing ${RANGE} with the Claude Code CLI…"
  RAW="$(llm_entry)"
fi

if printf '%s' "$RAW" | grep -q '<<<PUBLIC>>>' && printf '%s' "$RAW" | grep -q '<<<PRIVATE>>>'; then
  PUB_BODY="$(printf '%s\n' "$RAW" | sed -n '/<<<PUBLIC>>>/,/<<<PRIVATE>>>/p' | sed '1d;$d')"
  PRIV_BODY="$(printf '%s\n' "$RAW" | sed -n '/<<<PRIVATE>>>/,$p' | sed '1d')"
  # Trim leading/trailing blank lines.
  PUB_BODY="$(printf '%s\n' "$PUB_BODY" | sed '/./,$!d' | tac | sed '/./,$!d' | tac)"
  PRIV_BODY="$(printf '%s\n' "$PRIV_BODY" | sed '/./,$!d' | tac | sed '/./,$!d' | tac)"
else
  echo "gen-changelog: Claude CLI unavailable or output malformed; falling back to raw commit list"
  PUB_BODY="$COMMITS"
  PRIV_BODY="$COMMITS"
fi

# prepend_entry writes "# <heading>" + the new "## <version> — <date>" section on
# top of the file's existing entries (stripping the old top heading so it isn't
# duplicated).
prepend_entry() {
  local file="$1" heading="$2" body="$3"
  local existing=""
  if [ -f "$file" ]; then
    existing="$(sed '1{/^# /d;}' "$file" | sed '1{/^$/d;}')"
  fi
  {
    printf '# %s\n\n' "$heading"
    printf '## %s — %s\n\n' "$VERSION" "$DATE"
    printf '%s\n' "$body"
    if [ -n "$existing" ]; then
      printf '\n%s\n' "$existing"
    fi
  } >"$file"
}

prepend_entry "$PUBLIC" "Changelog" "$PUB_BODY"
prepend_entry "$PRIVATE" "Internal changelog" "$PRIV_BODY"

echo "gen-changelog: wrote ${VERSION} to ${PUBLIC} (public) and ${PRIVATE} (private)"
