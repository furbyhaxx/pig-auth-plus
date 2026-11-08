`claude` provider: Claude Pro/Max OAuth and Anthropic API key, streamed through `internal/wire` on PiG's `anthropic-messages`.

- Reference behavior: `host:/mnt/Projects/furbyhaxx/pi-auth-plus/src/families/anthropic/`.
- PiG's builtin API already adds the Claude Code identity block and maps tool names both ways for OAuth tokens; do not duplicate that here.
- Errors never carry tokens, codes or provider bodies (`AuthError`).
- `AccountHeader` is internal plumbing from ToAuth to the shaper; strip it before sending.
