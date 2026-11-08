`antigravity` provider: agy CLI's Cloud Code PA backend behind Google OAuth. It converts PiG requests to Gemini (`convert.go`), wraps them in the Cloud Code envelope (`request.go`) and maps the SSE stream to PiG events (`events.go`) itself.

- Not on `internal/wire`'s delegation route: PiG's Google adapter rejects a custom `fetch` (ANA-provider-delegation). Only `wire.Runtime.Ordered` is used, for system section order.
- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/families/google/`; agy findings: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/.project/analyses/antigravity-cli/`.
- Requests mimic agy (`transport.go`): bump `agyVersion` only after verifying a newer CLI.
- The Cloud Code project is discovered per session (`Projects.Reset` at session start); never persist it.
- Errors never carry tokens, codes, the project id or provider bodies (`ParseRPCError` redacts).
- Pushed events carry snapshots (`output.message`); never push a map that later deltas mutate.
- A rejected thought signature is repaired and retried exactly once (`RepairHistory`).
- The pi-antigravity-auth `google` slot migration is not ported: PiG's SDK has no locked auth.json write, and PiG users already hold an `antigravity` login.
