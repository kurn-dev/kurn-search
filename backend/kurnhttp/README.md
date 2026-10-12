# backend/kurnhttp

`kurnhttp` is the independent, fail-closed HTTP boundary between future
search adapters and an unmodified `kurnd`. It defines its own wire model and
does not import the Kurn Go module.

The transport surface is intentionally small:

- immutable clients, exact-string filters, one-list probes, and success
  values;
- 1..100 probes per call through the fixed `/v1/batch-query` route;
- exact request bounds and deterministic JSON bytes;
- strict store-stamp, list, resolved-config, filter-echo, and filter-stats
  evidence checks;
- full-batch refusal on any inline error or mixed evidence; and
- stable bounded error categories that retain no backend prose or request
  data.

Construction requires an absolute root `http`/`https` URL, a Bearer token,
an explicit response-byte ceiling, and an explicit maximum retry hint. The
client never follows redirects, sleeps, or retries. A valid bounded
`Retry-After` only annotates top-level 429/503 failures for a later policy
layer.

`StringFilter` remains scalar equality only. `MembershipFilter`
supports bounded exact-string alternatives with scalar or `in` canonical
encoding and a 16,384-byte ceiling. Each probe snapshots one selected mode's
canonical bytes for request marshaling; both nonempty modes refuse.
The standalone backend's original filterless case remains valid, but is not
a fallback for the new scoped membership executor.

Both filtered modes separately snapshot the exact response spelling produced by
the fixed daemon's default JSON encoder: `E(P) = json.Marshal(json.RawMessage(P))`,
where `P` is the trusted engine-canonical request. The response must match
`E(P)` byte for byte, including lowercase escaping of `<`, `>`, `&`, U+2028 and
U+2029 in names or values. No semantic normalization or alternate spelling is
accepted. The canonical request remains unchanged. This repairs the scalar
decoder's former refusal of valid fixed-daemon echoes for these characters.
It also intentionally refuses raw `P` when `E(P)` differs: the old scripted
scalar acceptance of that spelling is not a fallback. Unaffected ASCII filters and
membership verification retain their existing behavior.
`AppliedFilter()` returns a copy of the actual raw wire echo, which can differ
from `P`; its length is not the canonical admission size.

Standalone scalar limits remain eight terms, unique nonempty names of at most
128 Unicode runes and values of at most 512 runes (including empty), all valid
UTF-8. The 16,384-byte canonical cap applies to membership, not scalar filters.

Empty-value admission by `StringFilter` does not mean an empty member is stored:
the fixed public `kurn ingest` mapping drops empty text and omits that payload
member. A filter such as `{"empty":""}` therefore does not match a row whose
mapped payload lacks `empty`. The engine can match an actually stored empty
string; this is an ingestion distinction, not an empty-equality engine defect.
The parts service and its clients still require nonempty business-filter values.

For scalar filters the conservative **derived** canonical and echo ceilings
are each 30,769 bytes: `8 * (5 + 6 * (128 + 512)) + 7 + 2`. The corresponding
echo-only ceilings are 1,969,216 bytes for 64 probes and 3,076,900 for the
backend's 100-probe maximum. The adapter's limit remains 64 probes.

Membership's conservative **derived** echo-only ceiling remains 98,304 bytes
per filter, or 6,291,456 for 64 probes. All echo-only figures exclude the
envelope and candidates. Actual serialized request/response sizes and the
configured response-byte limit remain decisive; these figures do not promise
that every possible payload fits or establish live capacity. Adapter/client
admission limits are separate from standalone backend limits.

Boolean, numeric and opaque caller JSON filters remain excluded; no arbitrary
boolean language is exposed. Candidate payload stays
opaque valid JSON; trusted-payload interpretation belongs to a later adapter
layer.

This package proves a strict transport/evidence boundary only. It does not
provide adapter semantics, tenant-scope injection, ranking, evidence union,
saturation/completeness policy, an HTTP service, SDKs, or Elasticsearch
emulation.
