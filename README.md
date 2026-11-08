# pig-auth-plus

A [PiG](https://github.com/MichaelKinsy/PiG) extension for subscription and API-key providers,
quota monitoring and one `/auth` dashboard. It replaces
[pi-auth-plus](https://github.com/furbyhaxx/pi-auth-plus) on PiG.

## Features

- Providers: `claude` (Pro/Max or API key), `antigravity` (Google OAuth), `synthetic`.
  Login stays in PiG's `/login`.
- Quotas and credits for Claude, Antigravity, Synthetic, ChatGPT (`codex`), xAI, OpenRouter
  and DeepSeek, with a configurable status line and a live panel.
- Context token counts from the best source per model, with the source shown. Synthetic
  usage is reported in real tokens.
- `/auth` dashboard: providers, quotas, tokens, models (overrides and aliases), settings.
- Event bus `pi-auth-plus:v1:*` for other extensions (quotas, token counts).

Commands: `/auth`, `/quotas` (`/quota`), `/tokens` (`/token`). Login stays `/login`.

## Install

```bash
pig remove git:https://github.com/furbyhaxx/pi-auth-plus   # if installed
pig install git:https://github.com/furbyhaxx/pig-auth-plus
```

Settings live in `~/.pig/agent/auth-plus.toml` (builtin, user, session layers). An existing
`auth-plus.json` is imported once.

## Documentation

- [Getting started and migration](docs/getting-started.md)
- [Commands and keys](docs/commands.md)
- [Quotas: providers, status line template, resets, cache](docs/quotas.md)
- [Token counts: sources and Synthetic units](docs/tokens.md)
- [Models: overrides and aliases](docs/models.md)
- [Settings, files and environment](docs/configuration.md)
- [Event bus](docs/events.md)

## License

MIT, see [LICENSE](LICENSE).
