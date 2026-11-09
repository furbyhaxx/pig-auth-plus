# Quotas and credits

`/auth quotas`, `/quotas` and `/quota` open the same live view; the status line reads the
same cached reports. A missing number is unknown, shown as `?`, never zero. An explicit 0% is
real usage; an absent limit is not proof of unlimited access.

## Providers

Settings, commands and the bus use PiG ids. Aliases resolve to the same source; reports
always carry the PiG id.

| Id | Aliases | Compact | Reports |
|---|---|---|---|
| `claude` | `cl`, `cc` | `cl` | Subscription session (5h), weekly (7d), scoped weekly windows, spend caps, extra usage; response-header updates |
| `antigravity` | `agy`, `ag` | `ag` | Every reported quota bucket per group (Gemini, Argon, third-party pools) and reset times |
| `synthetic` | none | `syn` | Subscription request count, weekly plan budget, hourly search requests |
| `codex` | `openai`, `openai-codex`, `chatgpt`, `cx`, `cox`, `oai` | `cox` | ChatGPT primary/secondary windows, plan, reset-credit count; response-header updates |
| `xai` | `grok` | `xai` | Included period, positive on-demand cap and monthly included limit |
| `openrouter` | `or` | `or` | USD balance, account-credit usage, positive key cap |
| `deepseek` | `ds` | `ds` | Prepaid USD or CNY balance with granted and topped-up amounts |

Credentials come through PiG's model registry, including OAuth refresh; see
[sign in](getting-started.md#sign-in). A provider without a credential makes no request and
reports `needs-auth`. `opencode` and `opencode-go` have no quota source.

## Status line

`quotas.linePlacement`, or `p` in the Quotas tab:

| Placement | Location |
|---|---|
| `above-editor` | Widget above the editor |
| `below-editor-first` | Widget directly below the editor (default) |
| `below-editor-last` | One-row status entry in the footer area |
| `off` | Hidden; refresh and reporting continue |

One template applies at every width. Below `quotas.compactWidthCols` (default 100) identity
fields shorten; `quotas.alwaysShortIds` forces that. The line wraps at ` · ` or `{sep}`
boundaries, then inside oversized segments, and uses at most eight rows.

### Template reference

Write literal text and `{provider:field}` tokens in `quotas.lineTemplate`. Provider ids and
aliases are case-insensitive. Unknown tokens and invalid styles are ignored and listed in the
Settings preview.

| Field | Value |
|---|---|
| `id` | Provider spelling as written on wide terminals; compact id on narrow ones |
| `name` | Full display name |
| `short` | Compact id at every width |
| `tier` | Reported plan badge |
| `credits`, `credits-remaining` | Remaining balance with provider currency |
| `pct` | Primary important window percentage; follows `quotas.percentDisplay` |
| `wins` | Important windows (or the first two) |
| `rst` | Primary window's reset countdown |
| `bar` | Six-cell used-allowance bar |
| `resets-available` | Positive reset-credit count; empty for zero or unknown |
| `r` | `(r 1)` badge for a positive count |
| `window-<period>[-<scope>]-used-percent` / `-remaining-percent` | A specific window, e.g. `window-5h-used-percent`, `window-7d-gemini-4-argon-used-percent` |

`{provider}` renders its automatic summary; comma-separated fields combine
(`{claude:short,wins,rst}`). `{all}` renders enabled providers not placed elsewhere.
`{in-use}` renders the model's provider, running subagents' providers and any provider under
pressure (window at 90% used or critical balance); `{codex:in-use,id,pct}` is a gate.
`{sep}` is a preferred break. Square brackets make a conditional segment shown only when
every referenced value is known (`{codex:id}: {codex:window-7d-used-percent}[ ({codex:resets-available}r)]`).
Backslash escapes literal `[ ] { }`. Styles follow `|`:
`{claude:id|fg=#cba6f7,bold}`; `fg`/`bg` take theme tokens or hex, attributes are `bold`,
`italic`, `underline`, `strikethrough`.

With `quotas.hideSignedOut` (default) providers without a credential disappear from the line
and the Quotas tab, with the literal text of their segment. Before data arrives fields show
`…`, an error shows `err`, unknown metrics show `?`.

## Quotas tab

One row per provider with its important windows, balance, reset countdown and fetch age;
the row drops reset countdowns, then bars, then age as the terminal narrows, and never the
provider name or its state. Enter opens every window with its burn rate and projected
exhaustion; `/quotas <provider>` opens that detail directly. Opening the tab never fetches.
Keys are listed in [commands](commands.md#dashboard). The placement chooser asks for a save
scope (session or user) and saves nothing until you confirm it.

## Reset credits

Only `codex` supports reset credits: `x` on its row or `/quotas reset [provider]`. Credits
are listed first and the soonest-expiring is selected. **Keep the credit** is the first
choice; only **Spend 1 credit now (cannot be undone)** sends one consume request. A consume
is sent once, never retried, and is followed by a forced refresh. Dismissal, no UI or no
redeemable credit spends nothing. If a request fails mid-way the credit may already be
spent: list again before approving another. Reset requests over the [bus](events.md) need
the same in-session approval.

## Cache, refresh and drainage

`<agentDir>/cache/pig-auth-plus/quotas.json` holds snapshots, header observations,
drainage, refresh leases and backoff, shared by sessions on the same agent directory. Writes
are locked and atomic; corrupt or incompatible data is ignored. Refreshes use 30-second
leases against duplicate polling.

The engine checks every five seconds whether an enabled provider is due. Default interval
is `quotas.refreshIntervalSec` (60); Claude has a five-minute floor and Antigravity one
minute. After a turn, `quotas.refreshOnRequest` refreshes the active provider (minimum age
two seconds, floors apply). Rendering never fetches. With `PIG_OFFLINE` or `PI_OFFLINE` set
no automatic request is made. Everything stops with the session.

Failures keep the older data with an error. Generic failures back off 30 seconds; HTTP
403/429 use 60 seconds or `Retry-After`, capped at 15 minutes. A forced refresh bypasses
freshness, leases and ordinary backoff, but never an active 403/429 hold.

Claude's `anthropic-ratelimit-unified-*` and ChatGPT's `x-codex-*` response headers update
cached windows without an extra request; header time never postpones an endpoint refresh.

Drainage is an estimate from real observations: at least two observations spanning 60
seconds. Window rates are allowance per hour, balance rates money per hour. A top-up,
utilization drop or reset-time jump starts a new segment. ETA is remaining allowance over a
positive observed rate; a passed reset without new evidence suppresses it. These are linear
estimates, not predictions of provider policy.
