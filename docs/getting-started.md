# Getting started

pig-auth-plus adds subscription and API-key providers, quota monitoring, context token
counts and one `/auth` dashboard to PiG. It replaces the Pi package
[pi-auth-plus](https://github.com/furbyhaxx/pi-auth-plus) and cannot be loaded together with
it (both register the same provider ids).

## Install

```sh
pig remove git:https://github.com/furbyhaxx/pi-auth-plus     # if installed
pig install git:https://github.com/furbyhaxx/pig-auth-plus
```

Restart PiG. `/auth` opens the dashboard, `/model` picks a provider and model,
`pig --list-models` prints the catalog. If pi-auth-plus is still loaded, pig-auth-plus
refuses to start and prints the `pig remove` command.

## Sign in

Use PiG's `/login <provider>` and `/logout <provider>`. `/auth providers` shows credential
state and source; it never logs in or out. Credentials stay in `<agentDir>/auth.json`.

| Provider | Credential |
|---|---|
| `claude` | `/login claude`: Pro/Max OAuth with a pasted code, or an API key (typed, created through the console flow, or `ANTHROPIC_API_KEY`). Subscription quota needs OAuth. |
| `antigravity` | `/login antigravity`: Google OAuth, one account. Subscription turns list zero cost. |
| `synthetic` | `/login synthetic` with an API key, or `SYNTHETIC_API_KEY`. |
| `codex` (ChatGPT quota) | PiG's `/login openai-codex`; otherwise an existing pi-quotas `<agentDir>/quotas-codex-auth.json` is read (never written). |
| `xai` | PiG's `/login xai` with a credential the Grok CLI billing service accepts. |
| `openrouter` | PiG's `/login openrouter`; supplies credit and key-cap reporting. |
| `deepseek` | PiG's `/login deepseek`; supplies balance reporting. |

`claude`, `antigravity` and `synthetic` are inference providers owned by this extension.
The last four rows only add quota reporting to providers PiG already ships. `opencode` and
`opencode-go` stay PiG's built-in providers; anonymous OpenCode Zen is not supported.

## Migrating from pi-auth-plus

- **Credentials** in `auth.json` carry over unchanged; no re-login.
- **Settings**: on first start `<agentDir>/auth-plus.json` is imported into
  `<agentDir>/auth-plus.toml`, filling only keys the TOML file does not set. The JSON file is
  never modified or deleted. A marker in `$XDG_STATE_HOME/pig-auth-plus/migrations.json`
  records the import. Settings without a counterpart are reported once and skipped:
  `models.rules`, `models.groups`, `models.rulesScope`, `models.hiddenDefault`,
  `synthetic.webSearch`.
- **Antigravity**: an OAuth login stored under the `google` slot by pi-antigravity-auth is
  copied into an empty `antigravity` slot on first start.
- **Claude device id** from `$XDG_STATE_HOME/pi-auth-plus/claude-device-id` is adopted, so the account keeps one device.
- **Event bus** channels keep the `pi-auth-plus:v1:*` names; see [events](events.md).
- **Removed**: model groups, model visibility rules, the `--quotas-list`/`--quotas-json`
  CLI, the Synthetic web-search tool and flag, project-level settings.

## Next steps

- [Commands](commands.md): slash commands and dashboard keys.
- [Quotas](quotas.md): providers, status line template, resets, cache.
- [Tokens](tokens.md): how counts are built; Synthetic units.
- [Models](models.md): overrides and aliases.
- [Configuration](configuration.md): every setting, file and environment variable.
- [Events](events.md): reading quotas and token counts from another extension.
