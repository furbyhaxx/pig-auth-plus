# internal/tokens

Context token counts for the current session, each labelled with its source.

- `count.go`: `Source`, `Count`, `Sum` (weakest source wins; empty sum is an exact zero).
- `estimate.go`: characters-per-token estimates per route, UTF-16 length like the host.
- `segments.go`: host messages to countable segments; system-state replay. `anchor.go`: last valid usage on the branch.
- A missing number is unknown, never a guessed zero; estimates stay labelled `estimate`.
- No I/O here except the Claude count endpoint (own file); everything else is plain functions.
