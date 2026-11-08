---
document:
  type: "analysis"
  id: "ANA-final-verification"
  title: "Final verification sweep before cutover"
  description: "Gate results, live provider turns, persisted Synthetic usage and dashboard captures at four widths"
  status: "published"
  revision: 1
  created_at: "2026-11-08T23:30:00+01:00"
  updated_at: "2026-11-08T23:30:00+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["sweep on PiG 0.7.0+1.5.0"]
---

# Final verification sweep

- `just check` and `just race`: pass.
- Live one-tool-call turns (`read` of a file, reply with its content) with `pig --no-session -ne -e .`:
  `claude/claude-haiku-4-5`, `antigravity/gemini-3.8-flash`,
  `synthetic/hf:deepseek-ai/DeepSeek-V4.1-Flash-1017` all answered `hello-final`.
- Persisted Synthetic usage (session file): turn 1 input 81, cacheRead 8445, cacheWrite 0,
  total 8547; turn 2 input 142, cacheRead 8445, cacheWrite 0, total 8589. These are real
  tokens (the scaled stream would report about 42 % of that), and the duplicated cache write
  is gone.
- Dashboard captures (`/auth providers|quotas|tokens|models|settings`) at 36/57/80/140
  columns in `evidence/captures/`: no early exit, no line wider than its terminal.
- Known limit: the Tokens tab shows `estimate` for a fresh Synthetic session until the first
  reply anchors it (no pre-request count endpoint is usable for Synthetic).
