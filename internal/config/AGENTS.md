Settings: builtin defaults < user `<agentDir>/auth-plus.toml` < session (memory). No project layer.

- `catalog.go` is the single source of keys, defaults, ranges, enums, help and relations. New setting → `Catalog` entry + `Settings` field and `project()` line in `settings.go` + `docs/configuration.md`.
- BurntSushi/toml decodes; tomledit edits. Never re-serialize a whole document from a map; `document.apply` changes only edited keys and keeps comments, order and unknown keys.
- Table-list settings (`models.aliases`, `claude.replacePatterns`) are written as `[[key]]` tables, never inline tables (tomledit mis-indents inline tables inside arrays).
- Load: invalid values warn and inherit; out-of-range integers clamp with a warning; contradictory relation pairs fall back together. Save: out-of-range and contradictions are rejected, never rewritten.
- User saves: lock, re-read, apply edits, write atomically. A malformed file blocks saves and is never overwritten.
- Subscribers run after the lock is released; don't call `Save` from a subscriber synchronously.
