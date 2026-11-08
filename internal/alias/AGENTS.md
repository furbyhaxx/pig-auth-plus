Model aliases as PiG virtual models: `Plan` validates against the host's physical catalog, `Registry` keeps registered virtual models in step with `models.aliases`.

- An alias lists under its source's provider and routes every request to the source model; it inherits limits and thinking levels unless set.
- Problems are reported per alias and skipped; valid aliases stay registered. A plan sees only physical models, never earlier aliases.
- `Early` registers aliases from settings before the first session so `--model` and session restore resolve them.
