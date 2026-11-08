---
document:
  type: "specification"
  id: "SPEC-0001"
  title: "pig-auth-plus architecture and decisions"
  description: "What pig-auth-plus is, which PiG seams it uses, its package layout, config model, bus contract and the decisions that replace pi-auth-plus behavior"
  status: "draft"
  revision: 1
  created_at: "2026-11-08T18:45:14+01:00"
  updated_at: "2026-11-08T18:45:14+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["architecture, scope and decisions agreed with the user on 2026-11-08"]
---

# pig-auth-plus architecture

pig-auth-plus is a Go PiG extension that replaces the TypeScript package pi-auth-plus
(`host:/mnt/Projects/furbyhaxx/pi-auth-plus`) for PiG. It adds subscription and API-key
providers, quota and credit monitoring, accurate context token counts and one `/auth`
dashboard. Login stays in PiG's `/login`.

## Scope

In scope, with pi-auth-plus behavior as the functional reference unless a decision below
changes it:

- Providers: `claude` (Pro/Max OAuth or API key), `antigravity` (Google OAuth, Cloud Code),
  `synthetic` (API key, dynamic catalog).
- Request hooks on PiG builtins: OpenAI Responses verbosity, Anthropic cache retention.
- Quotas: `claude`, `antigravity`, `synthetic`, `codex`, `xai`, `openrouter`, `deepseek`;
  host-shared cache, refresh leases, backoff, response-header updates, drainage, codex reset
  credits with explicit approval.
- Quota line with the pi-auth-plus template grammar, placements and wrapping.
- Token counts per model with labelled sources; real-token usage for Synthetic.
- `/auth` dashboard: Providers, Quotas, Tokens, Models (catalog, overrides, aliases), Settings.
- Event bus channels compatible with pi-auth-plus `v1` consumers (pi-accordion, workbench).
- One-time import of `<agentDir>/auth-plus.json`.

Out of scope: model groups, model visibility rules, the read-only quota CLI, the Synthetic
web-search tool, the dedicated `/quota codex login` flow, project-scope settings.

## Decisions

| Decision | Reason |
|---|---|
| Go factory, extension name `pig-auth-plus`, module `github.com/furbyhaxx/pig-auth-plus` | PiG house standard; fusable into a Piglet Binary |
| Config file `<agentDir>/auth-plus.toml`, layers builtin < user < session | User decision 2026-11-08; no project layer |
| Saves preserve comments and unknown keys, lock, write atomically (0600), refuse malformed files | Config never loses data |
| Commands `/auth`, `/quotas` (`/quota`), `/tokens` (`/token`) | Drop-in replacement for pi-auth-plus |
| Providers delegate wire protocols to PiG builtin APIs (`anthropic-messages`, `google-generative-ai`, `openai-completions`) and shape traffic in a request-local fetch callback | Reuses PiG's tested SSE parsers; mirrors pi-auth-plus' request-local fetch without patching global state |
| A provider owns its usage: Synthetic's final message carries real input tokens and de-duplicated `cacheWrite`; cost is computed from the provider-reported units | pi-auth-plus' `message_end` correction never ran under PiG, so persisted messages and other extensions saw scaled units (pi-accordion ANA 2026-11-05 B4) |
| Synthetic real tokens: tokenizer when available, else measured per-model factor estimate, else character estimate; the source is labelled | User accepts mostly accurate estimates; tokenizer is preferred |
| Codex quota credential: PiG `openai-codex` login, then read-only fallback to `<agentDir>/quotas-codex-auth.json` | PiG ships the Codex OAuth flow |
| Aliases are PiG virtual models under the source provider | PiG has native virtual models |
| Bus channel names and payload shapes stay `pi-auth-plus:v1:*` | Existing consumers keep working; payload defects fixed (absent vs `null`, reused values) |
| OpenCode anonymous Zen is a gated spike, not a commitment | It never worked under PiG; needs a live probe |
| Per-session state lives in one session-owned struct, never keyed on event or context identity | pi-auth-plus pig-host root cause 2–4 |
| Request bodies are shaped as bytes, whoever serialized them | pi-auth-plus pig-host root cause 1 |

## Layout

```text
extension.go                 factory: registers providers, hooks, commands, events; thin wiring
internal/
  paths/                     agentDir, config dir, XDG state/cache (from the SDK, never hardcoded)
  config/                    catalog of settings, layered resolve, TOML round-trip save, import
  fsx/                       file lock, atomic write
  httpx/                     shared client, typed provider errors, redaction
  session/                   session-owned state and lifecycle (start, shutdown, replacement)
  providers/
    claude/                  oauth, identity, version, billing block, sanitize, tool names, fetch
    antigravity/             oauth, project, catalog, levels, envelope, signatures, schema, errors
    synthetic/               catalog, usage correction, interleaved folding
    hooks/                   openai verbosity, anthropic cache retention
  quotas/
    engine/                  scheduler, cache, leases, backoff, header overlay, drainage
    sources/<provider>/      one fetcher per quota source
    template/                line grammar parser and renderer
    line/                    placement and width fitting
    resets/                  codex reset credits
  tokens/                    tracker, anchor + tail, estimate, claude count API, tokenizer
  bus/                       pi-auth-plus:v1 quotas and tokens channels
  ui/                        dashboard frame and tabs (kit views)
```

Families never import each other; engines reach families only through registries.

## Bus contract

Channels: `pi-auth-plus:v1:quotas:{query,refresh,resets,reset,ready,snapshot,event}` and
`pi-auth-plus:v1:tokens:{query,ready,context}`, with the request and payload shapes of
pi-auth-plus `docs/events.md`. Absent optional fields are omitted, and decoders accept `null`
as absent. A `reset` request still requires in-session approval.
