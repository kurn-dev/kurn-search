# Multi-field parts-catalog discovery

`parts-catalog-discover` is the first example recipe meant to feel like an
application search box. It searches both the part `identifier` and the
human-readable `name`, while retaining the existing manufacturer, category,
status, tenant and optional partition constraints.

```json
{"query":"ball bearing","filters":{"manufacturer":["Northstar","Halden"],"status":"active"},"limit":25}
```

An exact identifier ranks first. Otherwise all deduplicated query terms must
match the same record. A single term can be satisfied by a prefix or by all of
its word pieces in `identifier`, or by the same alternatives in `name`.
Different terms may use different fields. Pieces of one hyphenated term may
not be split across fields, so `AX-bearing` is a valid complete query with no
matches in the checked fixture.

Terms of 3–20 Unicode scalars can use prefix matching. One- and two-scalar
terms, and terms longer than 20 scalars, use whole-word matching only. The
request admits 1–8 whitespace-separated terms, at most 80 scalars per term
and 256 scalars overall. Duplicate terms do not add probes. A term that yields
neither a prefix nor a word is refused. The computed plan may contain at most
64 probes.

The dedicated exact list contains domain-separated keys: exact identifiers,
identifier prefixes/words and name prefixes/words. The source `name` is
required, trimmed, 1–80 scalars and 1–8 whitespace tokens. Result payloads
contain the eight checked source fields, without scores or match-explanation
metadata.

The adapter uses Kurn-side membership filters before every candidate cut and
the fixed 128/256/512/1000 completeness ladder. A saturated ceiling refuses;
it never returns a silently truncated success. Ranking is exact identifier,
then all-whole-word matches before prefix-needed matches, then identifier,
name and ID in JavaScript UTF-16 order.

Follow the [discovery example](../../examples/parts-catalog-discover/README.md)
or the [user manual](../../docs/user-manual.md#multi-field-parts-catalog-discovery)
to build the checked list and call it from TypeScript or Python. This remains
a fictional loopback development recipe, not general full-text relevance or
production qualification.
