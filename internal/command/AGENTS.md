Argument parsing for `/auth`, `/quotas` and `/tokens` into typed `Request`s, plus nearby-name suggestions. No I/O, no SDK.

- A new section, alias or subcommand is added here and to `docs/commands.md` together.
- Unknown input returns an `Error` whose message is shown verbatim: usage line plus close matches.
