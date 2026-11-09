---
document:
  type: "analysis"
  id: "ANA-anthropic-api-credits"
  title: "Adding Anthropic API credit monitoring"
  description: "Repository integration analysis, verified public API constraints, private Console prior art and required validation"
  status: "published"
  revision: 2
  created_at: "2026-11-09T17:00:29+01:00"
  updated_at: "2026-11-09T22:31:36+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 2
      reason: "test existing authentication before requiring any extra credential"
      changes: ["recorded live OAuth and API-key identity probes, ambiguous zero-balance controls and explicit Console OAuth rejection"]
---

# Anthropic API credits

## Recommendation

Add a quota source named `anthropic`, labelled **Anthropic API**, without replacing PiG's builtin inference provider. Keep it separate from `claude` subscription windows and extra usage. Prefer existing credentials if they can retrieve an independently verified API-wallet balance; do not require a cookie merely because prior art uses one.

Live tests establish that the existing Claude OAuth login and Anthropic API key belong to the same account but different organizations. OAuth reaches the private OAuth prepaid endpoint, yet the endpoint also returns the same empty zero balance for a nonexistent organization. The Console browser route explicitly rejects the existing OAuth token. **No working, authoritative zero-extra-login balance path is established yet.**

Next verification needs only the balance shown on Console Billing (amount and currency), not a secret. If that differs from the OAuth response, reject this route rather than shipping a false zero. No runtime code, login, token refresh, purchase or credential mutation was performed.

## Verified constraints

| Interface | What the evidence establishes | Suitability |
|---|---|---|
| Public `/v1/organizations/analytics/cost` | Historical cost after discounts, **before credits**; decimal strings in cents [S1] | Spend reporting, not remaining credits |
| Legacy `/v1/organizations/cost_report` | Historical USD costs; separate usage endpoint [S2] | Not remaining credits |
| Spend Limits API | Enterprise member limits; unavailable to Console organizations [S3] | Wrong product and quantity |
| Console billing page | Available purchased credits and promotional credits; prepaid and invoiced accounts differ [S4, S5] | Authoritative UI, private transport |
| Private Console `/api/organizations/{uuid}/prepaid/credits` | Implemented in CodexBar with cookie authentication, amounts and grant arrays [S7]; existing OAuth rejected live [S11] | Cookie-authenticated balance still UNVERIFIED here |
| Private OAuth `/api/oauth/organizations/{uuid}/prepaid/credits` | Existing Claude OAuth accepted, but subscription/API/nonexistent organization requests all return the same empty zero response [S11] | Authentication accepted; API-wallet balance attribution UNVERIFIED |

Official docs say purchased credits expire; subscriber promotional credits expire at billing-cycle boundaries and are spent first. All keys/workspaces in the linked Console organization share the balance. Auto-reload considers purchased balance only. Some promotional credit is product-scoped and excludes Claude Code [S4, S5]. Therefore:

- Never infer remaining credit from a monthly spend cap, local token costs, or top-ups minus cost reports.
- Never assume all grants apply to every kind of inference request.
- Credit expiry is not a quota reset or a promised replenishment date.
- A postpaid account without prepaid data must not become a fabricated zero balance.
- Public balance absence is a research finding, not proof no undocumented endpoint exists. The open SDK request corroborates the gap, but is user-authored rather than a vendor contract [S6].

## Private Console prior art

Inspected repository: `steipete/CodexBar`, main at `7447bc77c249` (fetched 09.11.2026). Relevant source and tests [S7]:

- `docs/claudeplatform.md`
- `Sources/CodexBarCore/Resources/Plugins/claudeplatform.ts`
- `TestsPlugin/ClaudePlatformPluginTests.swift`

Its workflow uses `sessionKey` scoped to `platform.claude.com`:

1. `GET https://platform.claude.com/api/organizations`; filter `capabilities` containing `api`, excluding chat-only organizations.
2. `GET /api/organizations/{uuid}/prepaid/credits` for a readable API organization.
3. Optionally `GET /api/organizations/{uuid}/current_spend`; failures do not discard a good balance.

Source expects integer `amount`, optional `currency`, and optional `balance.credits.exponent`; it defaults absent currency/exponent to USD/2 and validates reported exponent in 0–4. Grant collections are `tranches` (purchased) and `promo_tranches`; fields include `remaining_amount_minor_units`, `granted_amount_minor_units`, `expires_at`, `program_id`, and `name`. Synthetic fixtures also contain `scope` and `amount_without_scoped_credits`; the plugin does not use them to calculate request eligibility. Do not copy that omission into an eligibility claim.

These are observations about another implementation, not verified server guarantees. Validate amount scale, absent-field behavior and currency using a live Console response before adopting defaults. A 2025 comment on SDK issue #505 independently reported the older `console.anthropic.com` route and a cents-valued `amount`, but it does not establish today's full schema [S9].

CodexBar picks the first readable API organization. **Recommended difference:** persist an explicit organization selection when multiple candidates exist; never silently move a configured source to another wallet after a 403. Do not aggregate organizations or assume the selected wallet matches a custom gateway's inference key. No automatic session-cookie refresh mechanism was established; manual renewal may be necessary.

## Live existing-credential investigation

Sanitized observation summary: [OAuth probes](anthropic-api-credits/evidence/oauth-probes.json) [S11]. Requests used locally stored credentials only in memory, with redirects disabled and bounded timeouts. Output omitted tokens, account/organization IDs, emails, names and raw bodies.

1. Existing PiG `claude` OAuth: `/api/oauth/profile` and `/api/oauth/claude_cli/roles` return 200; its organization is a Claude Max subscription.
2. Existing PiG `anthropic` API key: `/v1/models?limit=1` returns 200 and `anthropic-organization-id`. `/api/claude_cli_profile?account_uuid={OAuth-account}` independently confirms that organization, the same account UUID and `api_individual`/`prepaid` billing. No inference request was made.
3. OAuth prepaid balance requests for the subscription organization, discovered API organization and an all-zero nonexistent organization all return 200 with `amount: 0`, USD, exponent 2, and empty `tranches`/`promo_tranches`. Repeating with `x-organization-uuid` does not change the result. This proves an empty response is insufficient to authenticate wallet attribution; it does not prove the real wallet is nonzero or that the endpoint ignores every organization in every case.
4. The same OAuth endpoint rejects an API key (401) and no credential (401). OAuth authentication itself works; the unresolved issue is what the empty response represents.
5. Console organization discovery on both `platform.claude.com` and `api.anthropic.com`, and Console API-wallet balance on `platform.claude.com`, return 403: **This endpoint does not accept OAuth access tokens.** Do not retry with more spoofed headers or describe this as a missing token refresh.
6. The local standard Anthropic profile directory is absent and `ant` is not installed. No browser stores were read, new auth started, scopes escalated, keys created or tokens revoked.

Current installed Claude Code 2.1.317 was inspected with bounded memory-mapped string reads, not executed: its billing helper uses the OAuth prepaid route with `Authorization: Bearer`, `x-organization-uuid` and `anthropic-version: 2023-06-01`; the organization comes from its teleport identity. This is implementation evidence, not proof that a subscription identity can inspect Console API funds.

Official CLI documentation also describes a keyless Console browser OAuth sign-in, with automatic refresh [S12, S13]. It is a candidate for a one-time browser authorization rather than cookie export, but **billing-route compatibility is UNVERIFIED**. Do not use `claude auth login` as a shortcut: official docs say Console profile sign-in signs out the existing claude.ai login. Any integration must retain both identities separately and request no admin scopes without a concrete need.

### Immediate verification boundary

Compare the actual amount/currency on `https://platform.claude.com/settings/billing` with the OAuth response. A nonzero Console balance disproves the current zero response as an API balance. A matching zero is corroboration only; it still does not establish nonzero grant handling or reliable organization attribution. A fully working integration needs a positive or changing balance tied to the selected API wallet, or another independently verified wallet contract.

## Fit with this repository

Observed current behavior:

- `internal/app/quotas.go:20`: `newRegistry` registers seven sources, none named `anthropic`.
- `internal/app/quotas.go:62`: `providerAuth` resolves model-provider credentials; `authFrom` only extracts API keys or bearer headers. That does not provide Console cookies.
- `internal/quota/source.go:25`: `Source.AuthFrom` already supports source-specific credential resolution. Reuse this seam rather than changing global inference auth.
- `internal/quota/types.go:33`: `Balance` already supports remaining amount, currency and detail; total/used may remain nil.
- `internal/quota/sources/credits.go`: DeepSeek demonstrates remaining-only balances; existing HTTP errors feed scheduler backoff.
- `internal/app/authrows.go:18`: provider rows currently describe inference-provider credential state. An Anthropic API row must distinguish **inference configured** from **billing session configured/expired**.
- `internal/config/catalog.go:55`: default line explicitly enumerates sources. Registry registration alone will not add Anthropic to that template.
- `internal/quota/engine.go:348`: leases/cache entries are keyed by source ID, not organization. Switching wallets requires invalidation or an explicit non-secret scope discriminator before old balances can be shown as current.

### Smallest complete first slice

1. Add `internal/quota/sources/anthropic.go` and synthetic fixtures/regression tests. Source ID `anthropic`, `Credits: true`, no alias to `claude`, no header parser, no reset support. Leave inference registration untouched.
2. Resolve credentials through `AuthFrom`, preferring the existing `claude` OAuth plus `anthropic` API-key organization discovery only after API-wallet attribution is verified. Do not place OAuth-derived subscription or empty-default balances under `anthropic`. If separate billing credentials are needed, isolate them from inference auth. The read-only `sdk.ReadStoredCredential` helper alone is not a persistence API.
3. Keep login in PiG's `/login` if a separate billing auth method can be added without replacing builtin Anthropic auth. Otherwise obtain a UX decision before implementation; do not silently introduce a new login command or misuse `/login anthropic`.
4. Persist organization selection as non-secret user configuration, preserving TOML comments/unknown keys. Single-organization discovery can be automatic; multiple organizations require explicit selection. Never emit IDs or credentials on the bus.
5. Populate `Balance.Remaining` from the validated authoritative total and set currency; leave total and used nil unless their meanings are verified. Label it **Console API credit balance**, not subscription allowance or guaranteed funds for every request. Optional purchased/promotional breakdown belongs in `Detail` initially. Do not add invented monthly totals.
6. Register the source, add the provider/billing status row and update the default quota-line template without rewriting saved custom templates. Existing `quotas.providers` and `{anthropic:credits-remaining}` can use registry/template machinery.
7. Reuse scheduler cancellation, offline mode, leases, backoff and credit thresholds. Start conservatively with a proposed 60-second source floor; this is a design choice, not a documented private-endpoint allowance.
8. Update `README.md`, `docs/quotas.md`, `docs/configuration.md` and any affected bus documentation. No new inference provider or model catalog is needed.

Defer current-month spend, structured grant/expiry bus fields and expiry-specific widgets until balance authentication works. A richer grant contract would affect cache parsing, bus consumers and renderers; do not disguise expiry as `Window.ResetAtMs` just to reuse a countdown.

### Credential and transport requirements

- Prefer existing provider credentials. If a Console browser session is ultimately required, use explicit user-supplied or browser-authorized access; no automatic browser database/keychain scraping and no collection of account passwords.
- Secret input must not appear in slash-command arguments, shell arguments/history, notifications, fixtures or evidence. Never ask the user to paste a cookie into chat.
- If the chosen transport needs a dedicated billing credential, store it in a protected credential location, never `auth-plus.toml`, quota cache or inference auth. Missing billing access means zero billing HTTP requests even when an inference key exists. Existing-token discovery, if verified, must stay read-only and use its owner-approved endpoints.
- For a cookie-based transport, send only the required Console cookie to the pinned HTTPS Console origin; reject redirects to avoid login HTML and unintended cookie forwarding. Do not reuse arbitrary inference base URLs.
- Existing `httpx.Client` follows redirects and `httpx.Do` can include redacted upstream body text in errors. The Console transport needs origin/redirect enforcement and status-only errors for auth/permission failures; raw bodies can contain personal data even after token redaction.
- Differentiate expired session (401), denied organization billing access (403), rate limit (429), schema failure, and service failure. Preserve typed status/Retry-After handling; never retry authorization by substituting a different credential or wallet.
- Sanitize names/detail strings before terminal rendering. Never retain raw organization lists or full billing responses in shared state.

## Verification and readiness

Required tests for implementation:

- Real zero versus absent/malformed/non-finite amount; integer scaling and reported currency/exponent; remaining-only balances.
- Chat-only versus API organization selection; multiple wallets; configured wallet denied; no silent wallet switch.
- Purchased/promotional grants, product restrictions, exhaustion and expiry without invented reset semantics.
- Missing/expired billing credential despite valid inference key; inference credentials never sent to Console; Console cookie never sent to inference.
- Redirect refusal, safe errors, cancellation, offline mode, 403/429 backoff, wallet-switch cache invalidation.
- Registry/command resolution, configured-provider filtering, default line and preserved user templates, bus snapshot round-trip.
- `just check`; `just race` if concurrency changes; live `/quotas anthropic` plus `/auth` and tmux captures at 36/57/80/140 columns.

**Open prerequisite:** the actual Console Billing balance (amount and currency only), followed by wallet-attribution validation. Existing credentials have now been tested read-only; they were not changed or persisted anywhere new. If zero-extra-login access cannot be established, a separate browser authorization may be necessary, but its access to the correct balance must be tested before adding any login UX. Do not claim the reported empty zero is the real API balance.

## Sources

Accessed 09.11.2026. Official pages are primary for documented semantics; CodexBar source is primary evidence of its implementation, not a supported Anthropic API contract.

- **S1:** [Usage and Cost Analytics API](https://platform.claude.com/docs/en/manage-claude/usage-cost-analytics-api.md).
- **S2:** [Legacy Usage and Cost API](https://platform.claude.com/docs/en/manage-claude/usage-cost-api.md).
- **S3:** [Spend Limits API](https://platform.claude.com/docs/en/manage-claude/spend-limits-api.md).
- **S4:** [How do I pay for my Claude API usage?](https://support.claude.com/en/articles/8977456-how-do-i-pay-for-my-claude-api-usage).
- **S5:** [API credits for subscribers](https://platform.claude.com/docs/en/about-claude/api-credits-for-subscribers.md).
- **S6:** [Open balance endpoint request #1942](https://github.com/anthropics/anthropic-sdk-python/issues/1942), including a user report about promotional-credit accounting; corroboration, not vendor confirmation.
- **S7:** [CodexBar Console implementation](https://github.com/steipete/CodexBar/blob/7447bc77c249/Sources/CodexBarCore/Resources/Plugins/claudeplatform.ts), [tests](https://github.com/steipete/CodexBar/blob/7447bc77c249/TestsPlugin/ClaudePlatformPluginTests.swift), [documentation](https://github.com/steipete/CodexBar/blob/7447bc77c249/docs/claudeplatform.md).
- **S8:** Existing static research: `host:/mnt/shared/knowledge-base/claude-code-internals/evidence/raw/harness/claude-code/findings/04-undocumented-apis.md`, section 2, OAuth prepaid credits row. No live endpoint evidence.
- **S9:** [SDK issue #505, Console endpoint report](https://github.com/anthropics/anthropic-sdk-python/issues/505#issuecomment-2621325579).
- **S10:** [Admin API keys](https://platform.claude.com/docs/en/manage-claude/admin-api-keys.md): Console keys have broad access, not selectable read-only scopes; do not request one to solve an unsupported balance read.
- **S11:** [Sanitized live credential probes](anthropic-api-credits/evidence/oauth-probes.json), 09.11.2026. Observation summary only; no raw private responses retained.
- **S12:** [Claude Code authentication](https://code.claude.com/docs/en/authentication), especially keyless Console sign-in and its existing-login side effects.
- **S13:** [Official CLI authentication](https://platform.claude.com/docs/en/cli-sdks-libraries/cli/authentication.md), browser OAuth and separate admin profile guidance.
