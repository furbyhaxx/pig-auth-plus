---
document:
  type: "plan"
  id: "PLAN-0001"
  title: "Build pig-auth-plus"
  description: "Milestones, slices and acceptance for the Go re-implementation of pi-auth-plus for PiG"
  status: "draft"
  revision: 1
  created_at: "2026-11-08T18:45:14+01:00"
  updated_at: "2026-11-08T18:45:14+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["milestones M0-M7, regression checklist from pi-auth-plus findings"]
---

# Build pig-auth-plus

Architecture and decisions: [SPEC-0001](../../specs/0001-architecture.md). Functional
reference: `host:/mnt/Projects/furbyhaxx/pi-auth-plus` (`docs/`, `src/`, `.project/analyses/`).
PiG SDK: `pig reload --sdk-path`. Treat TypeScript as a behavior spec, not code to port line
by line.

## Progress

- Done: M0 foundation (paths, fsx, config, import, session lifecycle, conflict check).
- Now: M1.3 antigravity provider (claude and synthetic done).
- Next: M1.2 claude.

## M0 Foundation

Current state: empty repository.

1. Scaffold with `pig extension init . --lang go --name pig-auth-plus`; `go.mod`, `Justfile`
   (`check` = vet + golangci-lint + test, `validate` = `pig install . --validate-only --json`),
   root `AGENTS.md`, `README.md`, `.project` check.
2. `internal/paths`: agent dir and config dir from the SDK, XDG state/cache with defaults.
3. `internal/fsx`: advisory file lock (flock) with cancellation, atomic write 0600.
4. `internal/config`: typed settings catalog (key, default, kind, range, enum, relation such as
   `creditCriticalUsd < creditHealthyUsd`), resolve builtin < user < session with source per
   key, validation that warns and inherits, TOML round-trip save of changed keys only.
   Dependency to approve: a TOML library with comment-preserving edits.
5. Import `<agentDir>/auth-plus.json` once into `auth-plus.toml` (fill unset keys only, skip
   `models.rules`, `models.groups`, `synthetic.webSearch`), marker in XDG state.
6. `internal/session`: one session-owned state with cancellation; shutdown joins goroutines.
7. Conflict check: refuse to register and notify when pi-auth-plus is loaded (duplicate
   provider ids would fail anyway; give the `pig remove` command).

Done when `just check` and `just validate` pass and settings round-trip tests cover unknown
keys, comments, malformed files and concurrent saves.

## M1 Providers

1. **Spike**: a native provider whose `Stream` calls the host `anthropic-messages` stream with
   a `fetch` callback; verify body bytes, headers and streamed response pass through, and that
   the final message's usage can be replaced. Decide fallback (own SSE parser) if not.
2. **claude**: OAuth (PKCE, pasted code) and API key; refresh; Claude Code identity (version
   resolution, device id, session UUID, billing block, identity line, `metadata.user_id`,
   beta headers, `?beta=true`); tool names mapped out and restored in the stream; native MCP
   names kept; collision-safe for removed tools; `claude.replacePatterns`; catalog.
3. **antigravity**: Google OAuth; project discovery per session; catalog with grouped thinking
   levels; Cloud Code envelope over `google-generative-ai`; schema allowlist; thought-signature
   repair and one-attempt recovery; redacted errors; import of a `google` OAuth slot.
4. **synthetic**: API key; catalog refresh with bundled fallback and persisted last catalog;
   interleaved reasoning folding; usage correction (see M4.4).
5. **hooks**: `openai.verbosity` and `claude.cacheRetention` via `before_provider_request`,
   applied to the physical request only.
6. **OpenCode anonymous spike**: done, negative (429 from curl and Go with the full CLI identity); dropped, see SPEC-0001.

Done per provider: `--list-models`, `--model`, `/login`, `/logout`, session restore and a live
streamed turn with a tool call, captured with secrets redacted.

## M2 Quota engine and sources

Engine (cache `<agentDir>/cache/auth-plus/quotas.json`, locked atomic writes, 30 s leases,
60 s default interval, provider floors, 30 s/60 s/Retry-After backoff capped at 15 min,
forced refresh never bypasses a 403/429 hold, header overlay, drainage segments/ETA,
offline mode makes no request), then one source per provider, then codex resets with
explicit approval and single attempt. Missing credentials report `needs-auth` with zero
requests. Unknown numbers stay unknown.

## M3 Quota surfaces

Template parser and renderer (all fields, conditional brackets, styles, `{all}`, `{in-use}`,
`{sep}`), wrapping inside the width, placements (above, below first, below last/footer,
off, workbench segment if its API allows), Quotas tab with refresh, solo, placement chooser
and reset flow. Captures at 36/57/80/140.

## M4 Tokens

1. Tracker: anchor (last valid assistant usage) + tail, invalidated on model, thinking,
   compaction and branch changes.
2. Estimates: prose 4 and JSON 3 characters per token; labelled `estimate`.
3. Claude count endpoint for complete requests; memoized after auth shaping.
4. Synthetic real tokens: done in M1 (`internal/providers/synthetic/units.go`): measured
   stream factors per tokenizer repository, character estimate for unknown models
   (ANA-synthetic-usage-units). Re-measure factors when Synthetic adds models.
5. Tokens tab and fallback footer percentage with warning/error thresholds.

Done when a live Synthetic DeepSeek session shows persisted prompt tokens within 5% of the
non-streamed count and `cacheWrite != input`.

## M5 Dashboard

Frame and tabs with retained drafts and discard confirmation; Providers; Settings (typed
editors, staged values, save to user or session, shadowing feedback, Backspace reset);
Models (searchable catalog, override editor writing `models.json` with comments preserved,
alias editor). Headless: read-only text output.

## M6 Bus

Quotas and tokens channels per SPEC-0001; contract tests against pi-auth-plus payload
fixtures; verify pi-accordion receives token reports.

## M7 Release

User docs (`docs/`), live verification of every provider and quota source, captures,
uninstall pi-auth-plus and install pig-auth-plus on the user's PiG.

## Regression checklist (from pi-auth-plus)

- Body shaped as bytes, never only when a string (pig-host 1).
- No state keyed on event/context identity (pig-host 2–4); paths from the host (pig-host).
- Bus: `null` optional fields accepted, reused values not collapsed.
- Concurrent settings saves keep unrelated edits (F02); drafts survive tab changes, close
  asks (F03); offline mode makes no automatic requests (F05); login cancelled with session
  (F06); threshold contradictions rejected, not rewritten (F16); no cache writes after
  shutdown (F17); token report invalidated on model change (F18); missing credentials are
  login-required (F23); empty contributions are not "exact" (F24); typed collection editors
  (F25); placement save asks for scope (F26); hints follow rebinding (F27); command targets
  validated (F28); short terminals keep controls (F29); widths measured (F30).
- A removed tool name in the transcript must not break Claude requests.
- Synthetic: catalog refresh works under PiG; interleaved blocks folded; usage corrected in
  the persisted message.
