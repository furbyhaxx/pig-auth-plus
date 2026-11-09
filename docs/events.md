# Event bus

Other extensions read, refresh and observe quotas and read the session's token counts over
PiG's shared event bus (`ctx.Events()` in Go, `pi.events` in TypeScript). The channels and
payloads keep the pi-auth-plus `v1` names and shapes, so existing consumers (pi-workbench,
pi-accordion) work unchanged. Quota channels use `pi-auth-plus:v1:quotas:`, token channels
`pi-auth-plus:v1:tokens:`. Changes within v1 are additive; consumers ignore unknown fields.
Provider inputs accept [aliases](quotas.md#providers); outputs use PiG ids. Payloads contain
no credentials or account/project identifiers.

The services belong to the current session: registered at session start, removed on
shutdown, with no reply or broadcast after disposal. Do not treat a missing reply as zero
usage or approval. The bus emits without awaiting listeners: subscribe to the reply channel
first, then emit, and wait for a reply with your nonce.

## Request and reply

Send to `pi-auth-plus:v1:quotas:<operation>`:

```json
{ "nonce": "unique-request-id", "payload": { } }
```

Replies arrive on `pi-auth-plus:v1:quotas:<operation>:reply` with the same nonce:

```json
{ "nonce": "unique-request-id", "payload": { "v": 1, "ok": true, "report": { } } }
{ "nonce": "unique-request-id", "payload": { "v": 1, "ok": false, "error": "Recovery information" } }
```

The nonce is correlation, not authorization. A malformed envelope (no nonce) is dropped, so
callers need a timeout (15 seconds; 180 for `reset`, which waits for the user's dialog). A
malformed payload or an unknown or disabled provider gets an explicit `ok: false`. Optional
fields accept `null` like absence.

| Operation | Request payload | Success payload |
|---|---|---|
| `query` | `{ providers?: string[], maxAgeSec?: number }` | `{ v: 1, ok: true, report: QuotaReport }` |
| `refresh` | `{ providers?: string[], force?: boolean }` | `{ v: 1, ok: true, report: QuotaReport }` |
| `resets` | `{ provider: string }` | `{ v: 1, ok: true, resets: ResetCreditList }` |
| `reset` | `{ provider: string, creditId?: string, confirm: true }` | `{ v: 1, ok: true, outcome: ResetOutcome }` |

Omitted `providers` selects the enabled sources; `[]` selects none. Aliases are resolved and
duplicates removed; explicitly named disabled providers are rejected. `query` without
`maxAgeSec` reads cached state; a finite non-negative `maxAgeSec` refreshes older data
(minimum one second), respecting floors, leases and backoff. `refresh` attempts each source
with a zero age, still observing floors; `force: true` bypasses freshness, leases and
ordinary failure backoff, never an active 403/429 hold.

### Reset approval

`resets` is read-only. `reset` needs `confirm: true` and the user's explicit approval in the
owning session's dialog (it names the requesting extension; **Keep the credit** is first).
Missing or false confirmation, no UI, or a dismissed dialog refuses. `creditId` selects an
available credit, else the soonest-expiring one. The consume is sent once and never retried,
then a quota refresh follows even on failure. A bus timeout is not evidence that a credit
was kept: list again before any further attempt. Check the outcome's `code`.

## Broadcasts

Bare objects without envelopes.

- `pi-auth-plus:v1:quotas:ready`:
  `{ v: 1, providers: [{ id, aliases }], capabilities: ["query","refresh","resets","reset","snapshot","event"] }`.
  Emitted once at session start; later subscribers must query.
- `pi-auth-plus:v1:quotas:snapshot`: `{ v: 1, provider: ReportProvider }` for each changed
  provider view (also header updates). A current row, not a delta.
- `pi-auth-plus:v1:quotas:event`: one of
  `{ v: 1, kind: "auth-required", provider, message? }`,
  `{ v: 1, kind: "rate-limited", provider, status }`,
  `{ v: 1, kind: "reset-consumed", provider, code: "reset", windowsReset, requester }`
  where `requester` is `panel`, `command` or `bus`. Auth and rate-limit events fire on state
  transitions, not on every failed poll; `reset-consumed` only for a confirmed reset.

## Report shape

```typescript
interface QuotaReport { v: 1; generatedAt: number; providers: ReportProvider[] }
interface ReportProvider {
  id: string; title: string; short: string;
  state: "ok" | "stale" | "error" | "needs-auth" | "rate-limited" | "unknown";
  windows: ReportWindow[];
  resets?: { available: number };
  credits?: ReportCredits;
  tier?: string; needsAuth?: boolean; fetchedAt?: number; ageSec?: number;
  source?: "endpoint" | "headers"; error?: string;
}
interface ReportWindow {
  id: string; shortLabel: string; label: string;
  usedFraction?: number; valueText?: string; resetAtMs?: number; important?: boolean;
  group?: string; period?: string; scope?: string;
  usedPercent?: number; remainingPercent?: number; resetAt?: number; resetInSec?: number;
  drainage?: DrainageMetrics;
}
interface ReportCredits {
  remaining?: number; total?: number; used?: number; currency: string;
  detailText?: string; drainage?: DrainageMetrics;
}
interface DrainageMetrics {
  observedAt: number;
  window?: { perHour: number; coverageSec: number };
  h1?: { perHour: number; coverageSec: number };
  h24?: { perHour: number; coverageSec: number };
  etaSec?: number; resetsBeforeExhaustion?: boolean;
}
interface ResetCreditList { provider: string; fetchedAt: number; available: number; credits: ResetCredit[] }
interface ResetCredit {
  id: string; type: string; status: string; title?: string; description?: string;
  grantedAtMs?: number; expiresAtMs?: number; redeemedAtMs?: number; supportedByPlan?: boolean;
}
interface ResetOutcome {
  provider: string; code: "reset" | "nothing_to_reset" | "no_credit" | "already_redeemed" | "refused";
  creditId: string; windowsReset: number; reason?: string;
}
```

`reason` accompanies `refused` and explains what the provider reported; it never confirms a
spend.

Timestamps are epoch milliseconds; ages, countdowns, coverage and ETA are seconds.
`usedFraction` is 0-1, percentages 0-100; window drainage rates are fractions per hour,
credit rates currency units per hour. Missing numbers are absent, never zero; a real zero is
valid. `state: "ok"` means no provider-level failure, not that every optional metric is
known. A report is stale after more than twice the larger of the refresh interval and the
provider's polling floor.

## Token channels

`pi-auth-plus:v1:tokens:`; see [Token counts](tokens.md).

| Channel | Kind | Payload |
|---|---|---|
| `tokens:ready` | broadcast | `{ v: 1, capabilities: ["query", "context"] }` |
| `tokens:context` | broadcast | `{ v: 1, reason: Reason, report: ContextReport \| null }` |
| `tokens:query` | request | `{ refresh?: boolean, breakdown?: boolean }` |

The query uses the nonce envelope above; success is
`{ v: 1, ok: true, report: ContextReport | null, breakdown? }`. `report` is `null` before a
model is selected. Without flags it returns the latest cached count, `refresh: true`
recounts first, `breakdown: true` also counts the full request per contribution (several
provider calls on Claude). `context` is emitted when a recount finishes or the report is
cleared; a model or thinking change clears the old report first.

`reason` is one of `session_start`, `session_shutdown`, `model_select`,
`thinking_level_select`, `message_end`, `session_compact`, `session_tree`, `request`.

```typescript
type TokenSource = "provider" | "count-endpoint" | "tokenizer" | "estimate";
interface TokenCount {
  tokens: number; source: TokenSource;
  diagnostics?: { code: "needs-auth"; provider: string; message: string }[];
}
interface ContextReport {
  provider: string; model: string; tokens: TokenCount; contextWindow?: number;
  percent?: number; anchor?: TokenCount; tail: TokenCount; tailMessages: number;
  pi?: number | null; computedAt: number;
}
interface BreakdownPart {
  kind: "system" | "tools" | "user" | "assistant" | "toolResult" | "other";
  count: TokenCount; items: number;
}
```

`tokenizer` stays in the type for v1 compatibility; pig-auth-plus does not emit it.
`count-endpoint` describes the complete supplied request, not billed usage. Baseline-
subtracted Claude contributions are `estimate`. `diagnostics` explain why an authentication-
dependent count fell back and never contain credentials.
