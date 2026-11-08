---
document:
  type: "analysis"
  id: "ANA-synthetic-usage-units"
  title: "Synthetic streamed usage is scaled per model; non-streamed usage is real"
  description: "Live probe of every always-on Synthetic model: streamed prompt usage equals real tokens times a stable per-model factor, cache writes duplicate the input, and the factor table that converts it back"
  status: "published"
  revision: 1
  created_at: "2026-11-08T19:31:03+01:00"
  updated_at: "2026-11-08T19:31:03+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["stream vs non-stream vs tokenizer probe of 13 models"]
---

# Synthetic streamed usage units

## Summary

- **Non-streamed** `/anthropic/v1/messages` usage is the real prompt size: within 3–52 tokens
  (chat-template framing) of the model's public Hugging Face tokenizer.
- **Streamed** usage, which PiG reads, is the real size times a per-model factor. The factor is
  stable to ±0.1 % across prose and JSON, so dividing by it restores real tokens.
- Every response repeats the uncached input as `cache_creation_input_tokens`
  (`cacheWrite == input`), streamed or not.
- Supersedes the factor values of `host:/mnt/Projects/furbyhaxx/pi-auth-plus/.project/analyses/tokens/synthetic-counts/report.md`
  (2026-10-27), which measured only DeepSeek, GLM-4.7-Flash and gpt-oss; those three agree.

## Reproduction

2026-11-08 19:10–19:30 Europe/Zurich, key from `~/.pig/agent/auth.json` (never printed).
`evidence/probe3.py` sends the same 15 KB prose and 20 KB JSON text (each prefixed with a fresh
UUID so no prefix cache applies) once non-streamed and once streamed with `max_tokens: 1` to
every always-on model. `evidence/probe2.py` adds the model's tokenizer count
(`tokenizers` + `huggingface_hub` via `uv run`). Raw results: `evidence/factors3.jsonl`,
`evidence/factors2.txt`. `evidence/factors.jsonl` is the first, cache-contaminated run: repeated
texts on a shared backend report only the uncached tail.

## Factors (streamed prompt / real prompt)

Prompt = `input_tokens + cache_read_input_tokens` of the final `message_delta`.

| Tokenizer repository (`hugging_face_id`) | Models | Factor |
|---|---|---|
| `deepseek-ai/DeepSeek-V4.1-Flash-1017` | `hf:…DeepSeek-V4.1-Flash-1017`, `syn:large:text`, `syn:small:vision`, `infinite:small:vision` | 0.4212 |
| `moonshotai/Kimi-K3` | `hf:moonshotai/Kimi-K3`, `syn:large:vision` | 0.4210 |
| `moonshotai/Kimi-K3.1-Pro` | `hf:moonshotai/Kimi-K3.1-Pro` | 0.4207 |
| `zai-org/GLM-5.3`, `zai-org/GLM-5.3-Flash` | same ids with `hf:` | 0.4210 |
| `zai-org/GLM-4.7-Flash` | `hf:zai-org/GLM-4.7-Flash`, `syn:small:text` | 1.3560 |
| `openai/gpt-oss-120b` | `hf:openai/gpt-oss-120b` | 2.4430 |
| `Qwen/Qwen3.8-27B-FP8` | `hf:Qwen/Qwen3.8-27B` | 0.9392 |

`infinite:*` models refuse non-streamed requests; their factor is the DeepSeek backend's
(streamed values identical to `syn:small:vision`). Kimi tokenizers are not on the Hub as
`tokenizer.json`; their factor uses the non-streamed count as the real size.

## Consequences for pig-auth-plus

- The provider converts the final message's prompt usage with the factor of the model's
  tokenizer repository, after removing the duplicated cache write. Error < 1 %.
- An unknown repository has no trustworthy factor; the provider falls back to a character
  estimate of the request body and labels it.
- A pure-Go Hugging Face tokenizer is unnecessary for current models: it would only replace
  the factor for new, unmeasured models, and DeepSeek's pre-tokenizer uses a look-ahead
  (`\s+(?!\S)`) that Go's RE2 cannot express.
- Re-measure with `evidence/probe3.py` when Synthetic adds a model; the probe costs well under
  one cent.
