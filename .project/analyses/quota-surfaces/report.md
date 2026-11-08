---
document:
  type: "analysis"
  id: "ANA-quota-surfaces"
  title: "Quota line and Quotas panel live captures"
  description: "tmux captures of the quota line and /auth quotas panel at 36, 57, 80 and 140 columns and a 16-row terminal, with the defects found"
  status: "published"
  revision: 1
  created_at: "2026-11-08T21:00:00+01:00"
  updated_at: "2026-11-08T21:00:00+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["M3 verification against live provider data"]
---

# Quota line and Quotas panel

Run: `tui-capture.sh -w "36 57 80 140" -c "/auth quotas" -- -ne -e .` against the user's live
credentials (all seven sources signed in). Captures: `evidence/w*.txt`, short terminal in
`evidence/short/`.

## Observed

- No captured row exceeds its width at any size (checked with `awk length`).
- The line shows the seven-provider default template at every width; it wraps at ` · `.
- The panel keeps provider name and state first; bars shrink to 5 cells below 48 columns and
  the reset countdown is dropped first.
- At 16 rows the title, tab row, hint row and selected provider stay visible; the list scrolls.

## Defect found and fixed

At 36 columns two Antigravity windows rendered as `Claude and GPT mode…` with different
percentages: the period suffix `(7d)`/`(5h)` that tells them apart was cut. `fitLabel` now cuts
the middle and keeps the suffix; a unit test covers it.

## Not covered

- `/quotas reset` with a real credit (codex had none); covered by unit tests only.
- Placement chooser and `r`/`R` keys are unit-tested, not driven through tmux.
