Streams pig-auth-plus providers through PiG builtin wire APIs (ANA-provider-delegation).

- One private `sdk.ModelRuntime` per session (`Runtime`), isolated auth/model-store files under `paths.Wire()`; `Bind` the session Context at start, `Bind(nil)` at shutdown.
- Never route through `ctx.ModelRegistry()` with a public provider id: the host dispatches back to our own `streamSimple`.
- Wire provider names must never equal a public provider id.
- Shape HTTP only in the request-local `Fetch` exchange; never patch global transports.
- `Relay` is the only place a provider rewrites a final message; streaming deltas stay as the wire API produced them.
- `Runtime.Stream` relabels history to the wire provider (`adoptHistory`) and results back to the public provider (`Relay` with `Label`); without both, PiG drops thinking and signatures of earlier turns and persists the wire name.
