# Configuration

Use `/auth settings` to inspect effective values and their source layer, stage changes and
save them. Or edit the TOML file and start a new session; external edits are not watched.

## Layers

Each key resolves from lowest to highest priority:

1. Built-in default.
2. User file `<agentDir>/auth-plus.toml`.
3. Session: memory in the current session.

There is no project layer. `<agentDir>` is the agent directory PiG reports (`~/.pig/agent`
unless `PIG_CODING_AGENT_DIR` or `PIG_HOME` moved it).

```toml
[quotas]
providers = ["claude", "codex"]
linePlacement = "below-editor-first"
percentDisplay = "remaining"

[openai]
verbosity = "high"

[claude]
cacheRetention = "long"

[[claude.replacePatterns]]
match = "old phrase"
replacement = "new phrase"

[[claude.replacePatterns]]
match = "paragraph to remove"
```

Invalid values warn and inherit the lower layer. A missing file contributes nothing and a
malformed file warns and is never overwritten. Saves lock the file, change only the keys you
edited, keep comments, ordering and unknown keys, and write atomically with mode 0600. A save
reports keys still shadowed by the session layer. Backspace previews the built-in default
and stages removal of the key from the save scope. Session values vanish with the session.
An existing `auth-plus.json` is imported once, see [getting started](getting-started.md#migrating-from-pi-auth-plus).

## Settings

Numbers are rounded to integers and clamped to their range; booleans and enums must match
exactly.

| Key | Default | Effect |
|---|---|---|
| `dashboard.density` | `"comfortable"` | `"comfortable"` or `"compact"` row spacing |
| `dashboard.maxBodyRows` | `0` | 0-60; `0` fits the space, otherwise caps body rows |
| `dashboard.showProvenance` | `false` | Show alias sources and override/default labels in model detail |
| `quotas.refreshIntervalSec` | `60` | 15-3600 seconds; provider floors still apply; never disables polling |
| `quotas.refreshOnRequest` | `true` | Refresh the active provider after a turn |
| `quotas.providers` | `[]` | Provider ids or aliases; empty means all sources; a list with no known source enables none |
| `quotas.linePlacement` | `"below-editor-first"` | `"above-editor"`, `"below-editor-first"`, `"below-editor-last"`, `"off"` |
| `quotas.percentDisplay` | `"used"` | `"used"` or `"remaining"` for broad line metrics and panel percentages |
| `quotas.compactWidthCols` | `100` | 40-220; short identities below this width |
| `quotas.alwaysShortIds` | `false` | Short identities at every width |
| `quotas.hideSignedOut` | `true` | Omit providers without a credential from the line and tab |
| `quotas.creditHealthyUsd` | `50` | 5-500; balance at or above is healthy |
| `quotas.creditCriticalUsd` | `5` | 0-100; at or below is critical; must be below the healthy value |
| `quotas.lineTemplate` | seven-provider template | [Grammar](quotas.md#template-reference) |
| `models.aliases` | `[]` | [Aliases](models.md#aliases) |
| `openai.verbosity` | `"default"` | `"low"`, `"medium"`, `"high"` set `text.verbosity` on Responses requests; default leaves them alone; a model's own `samplingParams.text.verbosity` wins |
| `openai.verbosityProviders` | `["openai", "openai-codex"]` | Providers the verbosity setting applies to; empty disables it |
| `claude.replacePatterns` | `[]` | Literal rules: `match`, optional `replacement`; with one the system-prompt text is rewritten, without one paragraphs containing the match are dropped; not regular expressions |
| `claude.cacheRetention` | `"default"` | `"short"` (5 minutes) or `"long"` (1 hour) for every Anthropic Messages cache breakpoint of the selected model; models with `compat.supportsLongCacheRetention = false` keep PiG's request |
| `claude.cacheRetentionProviders` | `["claude", "anthropic"]` | Providers the retention setting applies to; empty disables it |
| `tokens.warningPercent` | `70` | 0-100; context percentage colour, strictly below the error value |
| `tokens.errorPercent` | `90` | 0-100; strictly above the warning value |

Contradictory pairs (credit thresholds, token thresholds) are rejected on save; conflicting
loaded values are ignored in favor of the inherited ones. Credit thresholds are display-only.

## Files

| Location | Purpose |
|---|---|
| `<agentDir>/auth.json` | PiG's credentials, shared with `/login` and `/logout` |
| `<agentDir>/models.json` | PiG's model overrides, edited by the Models tab |
| `<agentDir>/auth-plus.toml` | These settings |
| `<agentDir>/cache/pig-auth-plus/quotas.json` | Shared quota snapshots, header observations, drainage, leases, backoff |
| `<agentDir>/quotas-codex-auth.json` | Existing pi-quotas Codex credential; read only |
| `$XDG_STATE_HOME/pig-auth-plus/` | Migration markers; private wire-runtime files |
| `$XDG_STATE_HOME/pig-auth-plus/claude-device-id` | Claude device identity; adopted from pi-auth-plus' file of the same name when present, so the account keeps one device |

`XDG_STATE_HOME` defaults to `~/.local/state`. Private files use mode 0600 and directories
0700. Never put credentials into `auth-plus.toml`; it holds behavior only.

## Environment variables

| Variable | Effect |
|---|---|
| `ANTHROPIC_API_KEY` | API-key access for `claude` (subscription quota needs OAuth) |
| `SYNTHETIC_API_KEY` | Synthetic API key |
| `ANTHROPIC_CLAUDE_CODE_VERSION` | Overrides the Claude Code version subscription requests report; otherwise the newer of the bundled version and the installed `claude --version` |
| `ANTHROPIC_BASE_URL` | Custom Claude subscription origin; HTTP(S) without embedded credentials; replaces the origin only |
| `ANTHROPIC_INSECURE` | `1` or `true` skips TLS verification, only with an explicit `ANTHROPIC_BASE_URL`; avoid |
| `PIG_OFFLINE`, `PI_OFFLINE` | Disable automatic quota requests |
| `XDG_STATE_HOME` | State base directory |
| `HOME` | Home resolution for XDG defaults |
