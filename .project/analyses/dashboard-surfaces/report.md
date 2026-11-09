---
document:
  type: "analysis"
  id: "ANA-dashboard-surfaces"
  title: "Dashboard screens at 36, 57, 80 and 140 columns"
  description: "Live tmux captures of every /auth screen on the redesigned frame against the user's configured accounts, with the defects the captures exposed"
  status: "published"
  revision: 2
  created_at: "2026-11-08T21:10:48+01:00"
  updated_at: "2026-11-09T01:30:00+01:00"
  created_by: "orchestrator"
  updated_by: "orchestrator"
  changelog:
    - revision: 2
      reason: "dashboard redesign (fixed-height frame, detail screens)"
      changes: ["captures replaced: nine screens at four widths plus a 16-row terminal", "result and defect list rewritten for the new frame"]
---

# Dashboard screens at four widths

Run: `tui-capture.sh -w "36 57 80 140" -H 32 -c "/auth <tab>" [-k Enter|F1] -- -ne -e ./extensions/pig-auth-plus`
on pig 0.7.0+1.5.0 against the user's live accounts. Captures: `captures/<screen>-<width>.txt`
for providers, provider-detail, quotas, quota-detail, tokens, models, model-detail, settings
and help (36 files), plus `providers-57x16.txt` at a 16-row terminal.

## Result

- No rendered line exceeds its width (display width over all 37 captures).
- The frame is 18 rows from top rule to bottom rule on every screen at every width, so
  switching tabs or opening a detail never moves it.
- The key row keeps `f1 help` and the leave key at every width; at 16 rows the frame leaves
  room for Pi's footer and shows `↓ N more` in the bottom rule.
- Identity survives narrow widths: provider rows keep label and state glyph (`●`, `◐`, `○`)
  while source, plan, expiry and headline quota drop first; the tab bar scrolls and shows
  `n/5` at 36 columns.

## Defects the captures exposed, all fixed

| Defect | Fix |
|---|---|
| Models took 3 to 8 s to appear: sign-in state was read with one host call per provider (45 calls, 2.5 s) and each call re-reads the host's whole model state | One `GetAvailable` call; the list appears in under a second |
| The Models status read `0 models` while the catalog was loading | It reads `loading…`, or `failed` when the read failed |
| Inside a detail screen the leave hint read `close` | It reads `back` in a drill-in and `close` at the root |

## Not covered

- Colors: the captures are plain text. The chrome helper is unit-tested for contrast, thinking
  level mapping and a palette-less theme; every status pairs a glyph or word with its color.
- The reset dialog: the live account has no reset credits, so only unit tests cover it.
