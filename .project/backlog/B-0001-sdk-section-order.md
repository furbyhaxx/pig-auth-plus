---
document:
  type: "backlog-item"
  id: "B-0001"
  title: "PiG Go SDK loses system prompt section order in provider streams"
  description: "Remove the section-order workaround once the SDK hands providers an ordered sections value"
  status: "open"
  kind: "drift"
  priority: "P3"
  scope: ["internal/wire/sections.go"]
  revision: 1
  created_at: "2026-11-08T19:49:49+01:00"
  updated_at: "2026-11-08T19:49:49+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["found while porting antigravity"]
---

Current: the PiG 0.7.0 Go SDK decodes a provider stream's context into `map[string]any`
(`extensions/sdk/provider_dispatch.go`), so a system message's ordered `sections` object loses
its order, and passing the request back sorts sections by name. `internal/wire/sections.go`
restores the order from `ctx.GetSystemPrompt()` per stream.

Target: the SDK preserves the order (an ordered type for `sections`, or raw JSON for the
context); then delete `OrderSections`, `Runtime.Ordered` and their tests.

Why: without the workaround every claude and synthetic request had its system prompt
reordered (preamble after addendum, cwd and docs).
