`synthetic` provider: API key, public catalog, streamed through `internal/wire` on PiG's `anthropic-messages`.

- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/families/synthetic/`; evidence for usage units: pi-auth-plus `.project/analyses/tokens/synthetic-counts/report.md`.
- Synthetic reports prompt tokens in a scaled per-model unit and duplicates the uncached input as cache write. The final message's usage must carry real tokens: `finalize` dedupes, then rescales through `PromptCounter`. Output tokens and cost stay as reported.
- The catalog replaces, never overlays, the bundled snapshot; `catalog.json` is a captured `/v1/models` response.
- Tokenizer repositories come from the catalog's `hugging_face_id`, else from `hf:<org>/<repo>` ids.
