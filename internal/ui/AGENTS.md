Dashboard UI: `frame/` is the `/auth` shell (nine-line frame, fixed height, tab bar, hint row, modal choice, discard protection); each section is a `frame.Tab` in its own package. The design, keyboard and mouse standard is skill `extending-pig`, `references/dashboard-standard.md`; follow it and update it when a convention changes.

- Tabs return plain lines already fitted to `Env.Width`; check with `tui.VisibleWidth`, never `len`. Render does no I/O: tabs read cached state.
- Key hints come from `sdk.KeyHint`/`sdk.RawKeyHint`; match keys with `tui.GetKeybindings().Matches` or `tui.MatchesKey`.
- A modal choice from outside the panel queue goes through `Frame.Choose` in a goroutine; a choice that may close the panel uses the frame's own `then` path.
- Untrusted text (provider errors, model names) goes through `line.Plain` before styling.
- Layout changes are the user's call: show captures at 36/57/80/140 columns first.
