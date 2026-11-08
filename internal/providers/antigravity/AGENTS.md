`antigravity` provider: agy CLI's Cloud Code PA backend behind Google OAuth, streamed through `internal/wire` on PiG's `google-generative-ai`.

- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/families/google/`; agy findings: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/.project/analyses/antigravity-cli/`.
- Requests mimic agy (`transport.go`): bump `agyVersion` only after verifying a newer CLI.
- The Cloud Code project is discovered per session (`Projects.Reset` at session start); never persist it.
- Errors never carry tokens, codes, the project id or provider bodies.
- The pi-antigravity-auth `google` slot migration is not ported: PiG's SDK has no locked auth.json write, and PiG users already hold an `antigravity` login.
