Shared dynamic-catalog refresh policy: restore stored models, refetch after a TTL or when forced, persist `{models, extra, checkedAt}` through PiG's model store.

- Providers own parsing and fetching; this package owns only the restore/TTL/persist policy.
- A restored list is published before any fetch; a failed fetch keeps it and returns the error.
- Never use `sdk.CreateProvider` `FetchModels` for these providers: it overlays bundled models and re-stamps `checkedAt`.
