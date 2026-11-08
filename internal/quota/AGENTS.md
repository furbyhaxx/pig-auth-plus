Normalized quota model, cache, drainage and refresh engine. Sources live in `internal/quota/sources` (one file per provider, fixtures in `testdata/`); surfaces (line, panel, bus) only read.

- Unknown is nil, never zero. An explicit 0 is real usage; an absent limit is not "unlimited".
- Provider ids are PiG ids (`codex`, `xai`); aliases are resolved at the command boundary.
- Header observations overlay the endpoint snapshot but never move endpoint freshness (`Effective`).
- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/quotas/`.
