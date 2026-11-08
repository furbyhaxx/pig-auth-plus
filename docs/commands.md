# Commands

Login and logout remain PiG's `/login <provider>` and `/logout <provider>`.

| Command | Behavior |
|---|---|
| `/auth` | Open the dashboard on Providers |
| `/auth providers` | Credential state and source per provider |
| `/auth quotas [provider]` | Quotas tab; a provider starts in solo mode |
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

## Dashboard keys

Hints follow your configured bindings. Panel-owned shortcuts use letters or `ctrl+s`.

| Key | Action |
|---|---|
| Tab / Shift+Tab | Next / previous tab |
| Up/Down, PageUp/PageDown | Move or scroll |
| Esc | Cancel an edit, go back, or close; staged drafts ask before discarding |
| Help key (shown in the top rule) | Full descriptions and bindings, without losing drafts |

Tabs keep their staged edits when you switch away and back. Navigation is blocked while a
save is in progress.

**Quotas:** `k`/`j` select, `r` refresh the selected provider, `R` refresh all, `a` toggle
solo, `p` choose line placement (then an explicit save scope), `x` reset credit.

**Settings:** Left/Right (or `h`/`l`) toggle, cycle or nudge; Enter edits text, numbers and
opens list editors; Space toggles; Backspace stages the built-in default; `ctrl+s` saves to
`user` or `session`.

**Models:** `/` searches; Enter opens a model; `o` edits overrides, `a` adds or edits an
alias, `d` removes one.

**Tokens / Providers:** `r` recounts / rereads.
