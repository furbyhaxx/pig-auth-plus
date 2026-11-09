# Models

`/auth models` lists the models of the providers you are signed in to, with their context
windows. Providers without a credential are left out (sign in with `/login <provider>`).
Type to filter provider, id and name (all words must match); Enter opens a model. Overrides
and aliases are marked on the rows they change. Hiding models and routing groups are not part
of pig-auth-plus.

The `claude` catalog refreshes from Anthropic's account model listing through PiG's model
refresh lifecycle, with a four-hour freshness window. Prices come from models.dev's Anthropic
catalog, falling back to PiG's bundled price for the same model if unavailable. Compatibility
metadata comes from PiG, inheriting from the newest model in the same line when needed;
unknown lines are omitted. Models without a verified price are offered only for OAuth; PiG
restores its saved catalog offline and uses its bundled list before the first refresh. An
undated alias of a listed model (`claude-opus-4-5`) stays available, priced like its dated
id; the alias of a deprecated model is dropped. Open `/model` online to refresh;
`--list-models` only reads the saved or bundled list. OAuth subscription models still show
zero cost.

## Overrides

Enter on a model opens its fields. Name, context and output limits, reasoning, input types
and prices can be staged individually; Enter edits a field, Left/Right changes an option and
`alt+u` stages removal of that override. `ctrl+s` writes all staged fields in one locked
update; Esc asks before discarding them.

Overrides are written to PiG's user `<agentDir>/models.json` (`modelOverrides`). The editor
preserves comments, trailing commas and unrelated keys, refuses a malformed file and never
rewrites more than the touched values. An override changes how PiG lists, limits and prices
a model; it does not change what the provider enforces. Fields such as headers,
`thinkingLevelMap`, tiered costs and compat settings still need direct file editing.

## Aliases

In a model's detail, `+ New alias` opens the alias form and Enter on an existing alias row
edits it: an id (no spaces, not equal to the source and not taken in that provider), an
optional display name and optional context and output limits. Blank optional fields inherit
from the source. `Remove alias` (or `alt+d` on it) deletes one. The save asks for `user` or
`session` scope.

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
