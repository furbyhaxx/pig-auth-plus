---
document:
  type: "analysis"
  id: "ANA-dashboard-surfaces"
  title: "Dashboard tabs at 36, 57, 80 and 140 columns"
  description: "Live tmux captures of every /auth tab against the user's configured accounts, with the defects the captures exposed"
  status: "published"
  revision: 1
  created_at: "2026-11-08T21:10:48+01:00"
  updated_at: "2026-11-08T21:10:48+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 1
      reason: "document creation"
      changes: ["captures of providers, quotas, tokens, models and settings at four widths"]
---

# Dashboard tabs at four widths

Run: `tui-capture.sh -w "36 57 80 140" -H 32 -c "/auth <tab>" -- -ne -e .` on pig 0.7.0+1.5.0
against the user's live accounts. Captures: `captures/<tab>-<width>.txt` at 32 rows, and
`captures/<tab>-57x16.txt` at a 16-row terminal (25 files).

## Result

- No rendered line exceeds its width (checked by display width over all 25 captures), and every
  capture still shows the hint row with help and close, including 36 columns and 16 rows.
- Identity survives narrow widths: model ids are cut in the middle so the version tail stays
  (`anthropic.claude-sonne…250929-v1:0`), provider rows keep label and id, settings rows move
  the value below the key.
- The status line (quota line above the footer) wraps at every width.

## Defects the captures exposed, all fixed

| Defect | Fix |
|---|---|
| Tab strip cut the active tab at 36 columns (`Models…` hid `Settings`) | The strip keeps the active tab and the neighbors that fit, `…` marks a cut side |
| `/auth settings quotas.x` completion replaced the whole argument with the bare key, dropping the section word | Completion values carry the typed leading words; `/quota reset <provider>` had the same fault |
| Models tab stayed on "reading the model catalog…" forever | The catalog reload is started on open; a failed read is shown |
| Long model ids truncated at the tail, hiding the version | Middle cut |
| At 16 rows the panel claimed the whole terminal and the hint row and bottom border were pushed off screen | The panel leaves 6 rows for the footer and the quota widget |
| At 36 columns the tab hints filled the hint row and help and close were dropped | Help and close outrank every tab hint |

## Not covered

- Colors: the captures are plain text; themed output is checked by tests that every status
  pairs a glyph or word with its color.
