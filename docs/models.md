# Models

`/auth models` lists every model PiG can list, grouped by provider, with its context window.
`/` searches provider, id and name (all words must match); Enter opens a model's detail.
Overrides and aliases are marked on the rows they change. Hiding models and routing groups
are not part of pig-auth-plus.

## Overrides

Select a model and press `o`. Name, context and output limits, reasoning, input types and
prices can be staged individually; Enter edits a field, Left/Right changes an option and
Backspace stages removal of that override. `ctrl+s` writes all staged fields in one locked
update; Esc asks before discarding them.

Overrides are written to PiG's user `<agentDir>/models.json` (`modelOverrides`). The editor
preserves comments, trailing commas and unrelated keys, refuses a malformed file and never
rewrites more than the touched values. An override changes how PiG lists, limits and prices
a model; it does not change what the provider enforces. Fields such as headers,
`thinkingLevelMap`, tiered costs and compat settings still need direct file editing.

## Aliases

`a` on a model adds or edits an alias: an id (no spaces, not equal to the source and not
taken in that provider), an optional display name and optional context and output limits.
Blank optional fields inherit from the source. `d` removes an alias. The save asks for
`user` or `session` scope.

An alias is a PiG virtual model listed under the source's provider; requests go to the
source model. Aliases are stored in `models.aliases` of `auth-plus.toml`, and a higher layer's
list replaces the lower one:

```toml
[[models.aliases]]
provider = "antigravity"
source = "gemini-4-argon"
id = "fast"
name = "Fast"
```

Invalid aliases (missing source, collisions, duplicate ids) are reported once and skipped;
valid ones stay registered.
