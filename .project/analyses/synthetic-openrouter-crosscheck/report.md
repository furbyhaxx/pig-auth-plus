---
document:
  type: "analysis"
  id: "ANA-synthetic-openrouter-crosscheck"
  title: "Synthetic real-token counts cross-checked against OpenRouter"
  description: "pig-auth-plus' converted Synthetic prompt tokens compared with OpenRouter prompt_tokens for identical content, all always-on models, five prompt shapes plus a 22k-token context"
  status: "published"
  revision: 1
  created_at: "2026-11-08T21:50:58+01:00"
  updated_at: "2026-11-08T21:50:58+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["live comparison on 2026-11-08, no factor changes needed"]
---

# Synthetic real-token counts vs OpenRouter

status: "verified"
confidence: "high"

## Question

pig-auth-plus reports Synthetic prompt tokens as `reported / factor`, with the factor measured
per tokenizer (ANA-synthetic-usage-units). Is that number the real token count? An independent
reference is OpenRouter, which serves the same open-weight models from other hosts and reports
`prompt_tokens` in the model's real tokens. Synthetic is not an OpenRouter endpoint, so the
two services share nothing but the model.

## Method

`evidence/probe.py` sends the same content to both services and compares:

- Synthetic: Anthropic `/v1/messages`, streamed; prompt = `input + cache_read + cache_write`
  after dropping the duplicated `cache_creation_input_tokens`; divided by the factor in
  `internal/providers/synthetic/units.go` (read from the source at run time).
- OpenRouter: chat completions, `usage.prompt_tokens`, with the equivalent OpenAI-shaped
  messages and tool definitions.
- Cases: `short` (one question), `prose` (24 kB of docs), `json` (24 kB of catalog JSON),
  `tools` (seven Pi-style tool definitions), `session` (system prompt, tools, a tool call,
  a 7 kB tool result, two user turns), and `large` (300 kB of markdown, ~22k tokens).
- Models: all 13 always-on Synthetic ids (the `syn:*` and `infinite:*` aliases included) against
  OpenRouter `deepseek-v4.1-flash-1017`, `kimi-k3`, `kimi-k3.1-pro`, `glm-5.3`, `glm-5.3-flash`,
  `glm-4.7-flash`, `gpt-oss-120b` and `qwen3.8-27b`.

`evidence/results.json` has every pair; `evidence/providers.py` re-queries single OpenRouter
upstreams. Credentials are read from PiG's `auth.json` and never written.

## Result

Ratio = pig-auth-plus tokens / OpenRouter `prompt_tokens`, excluding the one-line `short` case.

| Synthetic model | Cases | Ratio range |
|---|---|---|
| `hf:deepseek-ai/DeepSeek-V4.1-Flash-1017` (+ `syn:large:text`, `syn:small:vision`, `infinite:small:vision`) | 6 + 15 | 0.9995 - 1.0025 |
| `hf:moonshotai/Kimi-K3` (+ `syn:large:vision`) | 6 + 5 | 0.9999 - 1.0121 |
| `hf:moonshotai/Kimi-K3.1-Pro` | 6 | 1.0008 - 1.0071 |
| `hf:zai-org/GLM-5.3`, `hf:zai-org/GLM-5.3-Flash` | 6 each | 0.9997 - 1.0015 |
| `hf:zai-org/GLM-4.7-Flash` (+ `syn:small:text`) | 6 + 5 | 1.0003 - 1.0014 |
| `hf:openai/gpt-oss-120b` | 6 | 0.9991 - 1.0000 |
| `hf:Qwen/Qwen3.8-27B` | 6 | 0.9997 - 1.0409 |

Of 60 non-short comparisons, 57 are within 1% and the mean ratio is 1.0018. Prose, JSON and
the 22k-token context agree within 0.1% for every model, so no factor drifts with size. The
raw streamed numbers differ from the real ones by 0.42x (DeepSeek, Kimi, GLM-5.3), 1.36x
(GLM-4.7-Flash) and 2.44x (gpt-oss), so the agreement is not a coincidence of near-1 units.

## Deviations

- **One-line prompts** (`short`): at most +5% (up to 5 tokens), from the provider's own
  framing tokens. Immaterial for context budgeting.
- **Tool definitions**: +0.1% to +0.7% on tool-only requests for Kimi and DeepSeek. OpenRouter
  upstreams render tool definitions into the chat template themselves.
- **Qwen3.8-27B with tools**: +4.1% (`tools`) and +1.3% (`session`). OpenRouter's own upstreams
  disagree by the same amount for the same request (863 to 941 tokens for `tools`, 2865 to 2943
  for `session`; `evidence/providers.py`), and our value 941 sits at the top of that range:
  the tool-template rendering differs per host, the tokenizer does not.
- **`syn:large:vision` `session`**: +1.2% against the one OpenRouter upstream that answered;
  Kimi-K3's own upstreams differ by up to 1% (2512 to 2536) on that request.

## Conclusion

The measured factors give real prompt tokens to within tool-template noise: 0.1% on text and
about 1% on tool-bearing requests, comparable to the spread between OpenRouter's own upstream
hosts. No change to `units.go` is needed. The check is repeatable: re-run `evidence/probe.py`
when Synthetic adds or changes a model; a model missing from the factor table falls back to
the character estimate and is not covered by this result.
