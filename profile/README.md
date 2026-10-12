# profile

Reserved for closed adapter descriptors: version and behavioral identities,
physical list identities, tested engine attestations, trusted filter policy,
and explicit query, pool, retry, timeout, and concurrency bounds.

Slice 2 implements that descriptor as an immutable validated value. It binds
an ordered closed evidence vocabulary, publish-only versioned physical-list
roles, exact resolved-config digests, disjoint trusted/caller filter fields,
the frozen five-module Go provenance, engine attestations, and named numeric
ceilings. The v1 query/probe/pool/result/version policy is exact; the only
silent default is `max_inflight=1`.

The behavior digest includes only frozen analyzer, codec, compiler, and
resolver output artifacts. Toolchain, modules, engine attestations, routing,
list names, and runtime policy remain deterministic operational descriptor
data and do not change the behavior digest. Diagnostic JSON is strict:
unknown or duplicate fields, noncanonical bytes, malformed identities, and
claimed/recomputed digest differences fail closed.

This package performs no registry lookup, authentication, admission,
attestation loading, backend execution, or service orchestration.
