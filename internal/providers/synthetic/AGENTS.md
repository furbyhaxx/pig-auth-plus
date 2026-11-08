`synthetic` provider: API key, public catalog, streamed through `internal/wire` on PiG's `anthropic-messages`.

- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/families/synthetic/`; evidence for usage units: `.project/analyses/synthetic-usage-units/report.md`.
- Synthetic reports prompt tokens in a scaled per-model unit and duplicates the uncached input as cache write. The final message's usage must carry real tokens: `finalize` dedupes, then rescales through `PromptCounter`. Output tokens and cost stay as reported.
- The catalog replaces, never overlays, the bundled snapshot; `catalog.json` is a captured `/v1/models` response.
- `streamFactors` holds measured factors (ANA-synthetic-usage-units); unknown repositories fall back to a character estimate, never to raw provider units.
- Tokenizer repositories come from the catalog's `hugging_face_id`, else from `hf:<org>/<repo>` ids.
