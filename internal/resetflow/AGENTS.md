Reset-credit flow for `/quotas reset`: list, pick, ask, spend once, report.

- Plain functions over small interfaces so the dialogs and the engine are fakeable; no PiG SDK imports.
- Nothing is spent without the explicit second choice. "Keep the credit" is first and dismissal keeps it.
- A consume is never retried; a failed or interrupted one says the credit may already be spent and points to listing again.
- Provider text is stripped of terminal sequences before it reaches a dialog or message.
