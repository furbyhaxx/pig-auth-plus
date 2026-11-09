# Token counts

pig-auth-plus counts the current session's context for the selected model and shows where
each number comes from. PiG's own count takes the last reply's usage and estimates the rest
at four characters per token; pig-auth-plus uses the best source the provider offers for the
part after that.

## How a count is built

```
context = anchor + tail
  anchor  total usage of the last valid assistant reply on the current branch
  tail    every message after it, counted with the model's counter
```

With no valid anchor (a new session, or after compaction or a context edit) the whole
request is counted: system prompt and tools, then messages. For Claude the native count
endpoint counts the complete shaped request. A valid usage anchor stays authoritative; the
tail and the breakdown are additive estimates.

Every count carries a source, most to least authoritative:

| Source | Shown as | Meaning |
|---|---|---|
| `provider` | exact (usage) | Usage the provider reported for a real request |
| `count-endpoint` | count API | The provider counted the supplied complete request; not billed usage |
| `estimate` | estimate | Measured characters per token (prose 4, JSON 3 unless measured per route) |

A sum takes the weakest source of its parts. An empty contribution is an exact zero; an
unknown one is not counted as zero.

## Per provider

| Provider | Anchor | Tail and first turn |
|---|---|---|
| `claude` | usage | Native count of the full request when supported; estimates otherwise |
| `synthetic` | usage in real tokens (see below) | estimate |
| `antigravity` | usage | estimate with measured ratios per model family |
| any other | usage | estimate |

## Synthetic reports real tokens

Synthetic's streamed usage is not a token count: input tokens arrive multiplied by a
stable per-model factor (about 0.42 for the DeepSeek, Kimi and GLM-5.3 models, 0.94 for Qwen,
1.36 for GLM-4.7-Flash, 2.44 for gpt-oss-120b), and the uncached input is repeated as `cache_creation_input_tokens`.
PiG would show 42% context use at the moment the provider rejects a request.

pig-auth-plus corrects the final assistant message before it is stored: prompt tokens are
divided by the model's measured factor, the duplicated cache-write is dropped, output
tokens and cost stay as reported. Models without a measured factor fall back to a character
estimate of the request, never to the raw scaled units. The factors were measured against
each model's published tokenizer and cross-checked against OpenRouter's `prompt_tokens` for
the same content: within 0.1% on text and about 1% with tool definitions, whose chat-template
rendering differs per host. The factors live in `internal/providers/synthetic/units.go` and
need re-measuring when Synthetic adds models.
Evidence: `.project/analyses/synthetic-usage-units/report.md`,
`.project/analyses/synthetic-openrouter-crosscheck/report.md`.

## Tokens tab

`/tokens` (alias `/token`) opens it.

| Row | Content |
|---|---|
| Context | The count as a bar, with its share of the context window |
| Source | Where the count comes from, as a glyph and a word |
| Pi | PiG's own count and the difference (Δ) |
| Anchor, Tail | The last reply's usage, and the messages after it |
| Breakdown | The full request split into system, tools, user, assistant and tool results |

The breakdown is counted when the tab opens and recounted on changes; on Claude its pieces
are labelled estimates. When native counting lacks a login, a recovery row names the
provider, endpoint and the `/login` command; estimates stay usable. Outside the TUI
`/tokens` prints the latest count without recounting.

The report is recomputed (debounced) after session start, model or thinking change, each
message, compaction and branch change. A model or thinking change clears the old report
first, so a stale count is never shown for another model.

`tokens.warningPercent` (70) and `tokens.errorPercent` (90) colour the percentage in the
tab; the warning value must be strictly lower than the error value.

## Event bus

Other extensions read the same reports through `pi-auth-plus:v1:tokens:*`; see
[events](events.md#token-channels).
