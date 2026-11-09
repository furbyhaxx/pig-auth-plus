# Commands

Login and logout remain PiG's `/login <provider>` and `/logout <provider>`.

| Command | Behavior |
|---|---|
| `/auth` | Open the dashboard on Providers |
| `/auth providers` | Credential state and source per provider |
| `/auth quotas [provider]` | Quotas tab; a provider opens its detail screen |
| `/auth tokens` | Context count, sources and full-request breakdown |
| `/auth models` | Model catalog, overrides and aliases |
| `/auth settings [key]` | All settings; a dotted key selects that row |
| `/quotas [provider]`, `/quota [provider]` | Quotas tab |
| `/quotas reset [provider]` | List reset credits, then ask before spending one |
| `/tokens`, `/token` | Tokens tab |

Section names are case-insensitive. Aliases: `providers` = `provider`, `status`, `accounts`,
`account`; `quotas` = `quota`, `usage`, `limits`; `tokens` = `token`, `count`, `counts`;
`models` = `model`; `settings` = `setting`, `config`, `options`. `resets` aliases `reset`.
Provider arguments accept PiG ids and the [aliases](quotas.md#providers). Unknown sections,
providers and settings keys are rejected with the usage line and nearby names.
Completions cover sections, providers, settings keys and the reset form.

Outside the TUI (print, JSON, RPC) read-only commands answer as text: Quotas prints cached
state without fetching, Tokens prints the latest count, Settings prints effective values and
their source layer, Models prints overrides and aliases. A reset without an interactive UI
only lists credits and spends nothing.

## Dashboard

One fixed-height panel with five tabs: Providers, Quotas, Tokens, Models, Settings.

```
── Auth › Providers ───────────────────────────── 9 providers · 7 signed in ──
 ❱ Providers ❱ Quotas ❱ Tokens ❱ Models ❱ Settings
 ⌕ Type to filter…

→ Claude        ● OAuth login · Max 20x · expires in 6h 12m        5h 6% · 7d 58%
  Synthetic     ● stored API key                                   sub 0% · 7d 5%
  OpenCode Go   ○ not signed in · /login opencode-go

──────────────────────────────────────────────────────────────────────────────
 Enter shows everything known about this provider. Credentials are never shown.
 ↑↓ move · enter details · alt+r reread · tab next tab · type to filter · f1 help · esc close
──────────────────────────────────────────────────────────────────────────────
```

The height stays the same when you switch tabs, filter or open a detail screen. The rules use
the current thinking-level colour; the active tab is bold on the accent background. A detail
screen adds a breadcrumb level (`Auth › Quotas › Claude`) and Esc returns to the same row.
`↑ N more` and `↓ N more` appear in the rules when rows lie outside the window. Hints follow
your configured bindings and drop the least important first on narrow terminals.

| Key | Action |
|---|---|
| Tab / Shift+Tab | Next / previous tab (Left/Right too, unless the row edits with them) |
| Up/Down, PageUp/PageDown | Move |
| Alt+Up / Alt+Down | Scroll one line without moving the cursor |
| Typing, or `/` | Filter the current list; Esc clears the filter first |
| Enter | Open, edit or confirm |
| Esc | Clear the filter, cancel an edit, go back, then close; staged edits ask before discarding |
| Ctrl+C | Close the panel (asks when edits are staged) |
| F1 | Help for the current tab; F1 or Esc returns |
| Ctrl+S | Save staged edits (asks for `user` or `session` scope) |

Mouse: the wheel scrolls, a click selects a row, a second click opens it, and the tab bar
is clickable. Every action also has a key.

**Providers:** Enter opens a read-only detail (credential, plan, sign-in and sign-out
commands, quota, models, token source, settings); rows marked `→ open in` jump to that tab.
`alt+r` rereads credential state.

**Quotas:** one row per provider. Enter opens every window with burn rate and projected
exhaustion. `r` refreshes the selected provider, `R` all, `p` chooses the line placement
(then a save scope) and `x` starts a reset-credit request. This list does not filter on
typed text because those letters are actions.

**Tokens:** `r` recounts. Up/Down scroll.

**Models:** only models of providers you are signed in to are listed; type to filter. Enter
opens a model: Enter edits a field, Backspace resets an override, `ctrl+s` saves overrides
to `<agentDir>/models.json`. The detail has `+ New alias` and a row per existing alias.

**Settings:** Left/Right or Space toggle, cycle or nudge; Enter edits text and numbers and
opens the provider-list, prompt-rule and line-template editors; Backspace stages the
built-in default; `ctrl+s` saves. Each row shows where its value comes from (`default`,
`user` or `session`) and `●` marks a staged edit.

Tabs keep their staged edits when you switch away and back. Navigation is blocked while a
save is in progress.
