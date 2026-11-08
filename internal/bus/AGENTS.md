Event-bus contract `pi-auth-plus:v1:*` for other extensions: quota and token channels.

- Wire format is fixed by v1 consumers (pi-workbench, pi-accordion): nonce envelope on `<op>`, reply on `<op>:reply`, broadcasts are bare objects. Changes within v1 are additive only.
- No PiG SDK imports: the service talks to a `Transport`, the quota engine and the token tracker through small interfaces so it is testable with fakes.
- Replies and broadcasts stop with the session context; nothing is sent after disposal.
- Optional request fields accept `null` like absence. A malformed envelope (no nonce) is dropped; a malformed payload gets an explicit `ok: false` reply.
- A reset needs `confirm: true` and the user's approval through the injected `Approver`; resets are serialized and never retried.
- Payloads carry no credentials, credit ids in broadcasts, or account/project identifiers.
