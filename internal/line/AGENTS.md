Quota line template: parser, renderer and row packing. Pure: no I/O, no clock, no SDK UI calls; the host theme sits behind the `Theme` interface.

- Grammar and field list: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/docs/quotas.md` (Template reference). Keep it compatible.
- Unknown values render `?`, never 0. A signed-out provider (`hideSignedOut`) drops its whole ` · ` segment, literal text included.
- Every packed row fits the width; check with `tui.VisibleWidth`, never `len`.
- Provider text is stripped with `Plain` before styling or measuring.
