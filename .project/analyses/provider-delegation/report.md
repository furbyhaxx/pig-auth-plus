---
document:
  type: "analysis"
  id: "ANA-provider-delegation"
  title: "Delegating provider streams to PiG builtin APIs from Go"
  description: "Spike result: how a Go extension provider reuses PiG's builtin wire protocols with a request-local fetch, and which routes fail"
  status: "published"
  revision: 1
  created_at: "2026-11-08T19:02:45+01:00"
  updated_at: "2026-11-08T19:02:45+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["spike against pig 0.7.0+1.5.0 with a fake SSE transport"]
---

# Delegating provider streams to PiG builtin APIs

Spike source: `evidence/spike-extension.go.txt` (run with
`pig --no-extensions -e /tmp/pap-spike -p "say hi" --model pap-spike/m`).

## Working route: private ModelRuntime with a wire provider

1. The public provider is registered natively: `sdk.CreateProvider` (with `Auth` for API-key and
   OAuth login methods) plus `Extension.RegisterNativeProvider`. Its stream function has no
   `sdk.Context`, so it uses the Context retained from `session_start` (a retained Context stays
   valid until the session is replaced). The Pi-style `RegisterProvider(id, {streamSimple})`
   form works the same way but offers only OAuth login.
2. Its `streamSimple` lazily creates one private `sdk.ModelRuntimeCreate(ctx, {NoModelsFile: true,
   RefreshOnCreate: false, AuthPath: <private file>})` and registers a provider `wire` there with
   `api: "anthropic-messages"` (or another builtin API), a base URL and the same model ids.
3. It calls `runtime.StreamSimple(model{provider: "wire"}, request, options+{apiKey, fetch})`.

Observed: the builtin serializer built the full Anthropic body (`/v1/messages?beta=true`, cache
control, system blocks), the `fetch` callback received method, URL, headers and body, and the
faked SSE response streamed back as the final answer (`spike-ok`). A tool-use turn followed by a
second request worked with the runtime created once and reused across requests. The private runtime's models never appear in `--list-models`. A second spike repeated this with the
native form (`/tmp` spike, same fake SSE): the transcript arrives as `{messages}` (system prompt
and tools ride inside PiG 1.5 messages), options carry `apiKey`, `sessionId`, `timeoutMs`,
`maxRetryDelayMs` and a `Signal`.

The inner stream has no abort handle; cancel it by cancelling the fetch request, so the fetch
must join `ProviderStreamOptions.Signal` into the request context.

## Failing routes

| Attempt | Result |
|---|---|
| `ctx.ModelRegistry().StreamSimple` on the provider's own id | The host dispatches by provider id to the same `streamSimple`: unbounded recursion (35k calls before kill) |
| Session registry, builtin `anthropic` provider with a known id | Works, but only for ids in PiG's anthropic catalog |
| Session registry, builtin provider with an unknown id | The host builds a generic model and sends OpenAI chat completions to `api.openai.com` |
| A second public "wire" provider | Works, but its models are listed in `/model` and `--list-models` |

The host resolves a stream model only by `provider/id` (`subprocessModelSpec`); the `api`,
`baseUrl` and other fields of the model map are ignored. The Node runtime's `apiRequest` path,
which streams an arbitrary model object, is not exposed by the Go SDK 0.7.0.

## Consequences

- Every pig-auth-plus inference provider uses the private-runtime route; the `fetch` callback is
  the single place to shape requests and rewrite responses.
- A recursion guard is unnecessary on this route but the public provider id must never be
  registered in the private runtime.
- PiG's builtin `anthropic-messages` already maps Claude Code tool names both ways and adds the
  identity block when the API key is an OAuth token (`ai/anthropic.go` `toClaudeCodeName`,
  `anthSystemBlocks`), so `claude` adds only the billing block, `metadata.user_id`, version
  headers, replace patterns and fingerprint removal.
