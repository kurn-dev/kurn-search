# Private artifact and route lifecycle seam

This package implements the local W5-S3 conformance exerciser. It verifies an
S2 `BUILT-UNATTESTED` candidate, sends one length-framed canonical envelope to
an exact external verifier, registers accepted bundle bytes in an immutable
private filesystem registry, and appends guarded route decisions.

The repository's fixture verifier admits only `fixture-checksum/v1`. That proof
is a non-secret checksum and is not cryptographic authentication. Its maximum
claim is `EXTERNAL-VERIFIER-ACCEPTED` private composition. Production trust
roots, keys, signing, hosted registries, platform route authority, fleet
convergence, TLS, IAM, KMS and deployment remain unavailable and user-gated.
