In-place editing of PiG's `models.json` `modelOverrides`; keeps comments, trailing commas, unknown keys and layout.

- `stripped` blanks comments and trailing commas by spaces so offsets stay equal; parse and validate on that copy, splice into the original.
- Only the touched value changes. A malformed file is refused and left untouched; writes lock and go through `fsx.WriteAtomic`.
- Removals prune only the override objects they empty (model entry, `modelOverrides`), never the provider.
