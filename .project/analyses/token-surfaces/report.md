---
document:
  type: "analysis"
  id: "ANA-token-surfaces"
  title: "Tokens tab and Claude native count, live"
  description: "tmux captures of /tokens at 36, 57, 80 and 140 columns, plus a live check that the Claude count endpoint reproduces provider usage"
  status: "published"
  revision: 1
  created_at: "2026-11-08T20:38:39+01:00"
  updated_at: "2026-11-08T20:38:39+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["M4 verification against live data"]
---

# Tokens tab and Claude native count

Captures: `evidence/w36.txt` to `evidence/w140.txt` (`/tokens` with
`synthetic/hf:deepseek-ai/DeepSeek-V4.1-Flash-1017`, empty transcript). No row overflows.

## Claude native count (claude-haiku-4-5, subscription login)

| Step | Result |
|---|---|
| Real turn "reply with the single word ok" | usage anchor 10,381 tokens; Tokens tab shows `exact (usage)`, delta +0 against the host |
| Fresh session, empty transcript, `/tokens` | `estimate` 14,118: the count endpoint answers 400 `messages: at least one message is required`, so an empty transcript is never counted natively |
| Prompt sent then aborted (no usage), `/tokens` | `count API` 10,381, identical to the usage of the real turn; the host's own estimate said 9,990 (-3.8 %) |

Findings

- The captured body (serialized by PiG after this package's shaping, billing block removed)
  is accepted by `/v1/messages/count_tokens` and reproduces billed usage exactly in the one
  compared case. One sample: UNVERIFIED for other models or thinking levels.
- Defect found and fixed: the empty-transcript request put the counter into a one-minute
  backoff. The counter now skips native counting without messages before any request.
- The breakdown rows stay estimates: system 12,355 plus tools 1,761 exceeds the true 10,381
  total by 35 % because the Claude prose ratio (2.73) was measured on conversational text, not
  on the system prompt. They are labelled `estimate`.

## Decisions

- No fallback footer: PiG's footer shows its own context percentage, which for Synthetic is now
  corrected at the source (ANA-synthetic-usage-units). `tokens.warningPercent` and
  `tokens.errorPercent` colour the Tokens tab total, always with a glyph.
