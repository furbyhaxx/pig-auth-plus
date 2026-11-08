Pure payload rewrites for `openai.verbosity` and `claude.cacheRetention`; wired by `internal/app/hooks.go` on `before_provider_request`.

- Return nil when nothing changes; never mutate the input payload.
- Match `payload.model` to the selected model so helper requests with other models stay untouched.
- Retention moves every breakpoint together (Anthropic rejects a 1h breakpoint after a 5m one).
- PiG leaves the request purpose empty outside the cache-warming stream path: empty counts as a turn.
