# AGENTS.md — pig-auth-plus

Go factory extension for PiG that replaces the TypeScript package pi-auth-plus: providers
`claude`, `antigravity`, `synthetic`; request hooks on PiG builtins; quota/credit monitoring
and line; per-model context token counts; one `/auth` dashboard. Login stays in PiG's `/login`.

## Layout
- `extension.go` — `Extension()` factory, package `authplus`. Registration and wiring only.
- `internal/` — feature packages, each with its own AGENTS.md: `providers/{claude,antigravity,synthetic}` (inference), `wire` (private runtime on PiG builtin APIs), `quota` + `quota/sources`, `line` (template), `tokens`, `bus`, `config`, `alias`, `modelsfile`, `hooks`, `resetflow`, `migrate`, `ui/*` (dashboard tabs), `app` (SDK wiring). Providers never import each other.
- `docs/` — human-facing product docs. New command, setting or provider → update `README.md` and the matching doc in the same change.
- `.project/` — development artifacts; never in `docs/`; committed. Load skill `dot-project` before writing, revising or deleting one. Only what currently applies or is planned stays; superseded artifacts are deleted with `Artifact-*` commit trailers, never archived. Do not read deleted artifacts from git history unless asked. Open work: `BACKLOG.md` pointers + `.project/backlog/`; live claims: project-scope `tasks`. Never put drift/TODOs in AGENTS.md.
- `scripts/check_artifacts.py` — `.project` structure check (from skill `dot-project`).
- `.tmp/` — generated modfile; git-ignored.

## Sources of truth
- Architecture and decisions: `.project/specs/0001-architecture.md`. Work, slices, regression checklist: `.project/plans/0001-build-pig-auth-plus/plan.md`.
- Functional reference (read-only): `host:/mnt/Projects/furbyhaxx/pi-auth-plus` (`docs/`, `src/`, `.project/analyses/` for captured wire evidence). Port behavior and tests, not TypeScript structure or its Pi-host workarounds.
- SDK: `pig reload --sdk-path`; its doc comments win over the docs bundle. Load skill `extending-pig` for extension work.
- Sibling conventions: `host:/mnt/Projects/furbyhaxx/pig-workbench` (config, Justfile, host adapters).

## Commands
- Gate: `just check` (gofmt, vet, test, validate, artifacts). Concurrency changes: `just race`.
- Don't run bare `go build`/`go test`; the SDK resolves only through the staged copy. Use the Justfile or `-modfile=.tmp/go.mod` after `just _modfile`.
- Add a dependency: `just get <module>@<version>`.
- Load live: `pig -e .`; isolated: `pig --no-session -ne -e .`.

## Identity
- Extension name `pig-auth-plus` = directory name = `name` in `extension.go`.
- Commands: `/auth [section]`, `/quotas` = `/quota`, `/tokens` = `/token`. No `/pig-auth-plus` family.
- Config: `<agentDir>/auth-plus.toml`; layers builtin < user < session. No project layer.
- Bus channels stay `pi-auth-plus:v1:*` for existing consumers.
- Cannot co-load with pi-auth-plus (duplicate provider ids).

## Rules
- Per-session state lives in one session-owned struct. Don't key state on event payloads or `sdk.Context` values; PiG snapshots them per handler.
- Shape request bodies as bytes, whatever serialized them.
- Quotas and token counts are reported by their owner or shown as unknown; never interpolate or default a missing number to 0. Label every count's source.
- No credential in any log, notification, bus payload, fixture, doc or commit. Redact before writing evidence.
- Live verification is authorized on the user's configured accounts for these providers and their quota/count endpoints. Irreversible calls (spending a reset credit, creating keys, revoking tokens) need explicit approval each time.
- Panel, line or renderer change → tmux captures at 36/57/80/140 columns (skill `extending-pig` script).
- Runtime dependencies: check latest version and maintenance first; record licenses in `THIRD_PARTY_NOTICES.md`.
