# Private fixture fleet seam

Package `fleet` is the standard-library-only W5-S4 local conformance seam. It
strictly plans an exact S3 artifact and ACTIVE route, stages the immutable
bundle to at least three private local node roots in the reviewed
`config.json`, `base.jsonl`, `base.idx` publication order, derives
domain-separated full identities, emits allowlisted management-only
observations, records a predecessor-bound fixture audit decision, and restores
the exact accepted state into a new isolated root.

The executable surface is exactly:

```text
kurn-search-fleet plan     -spec <fleet.json> -registry <registry-dir> -out <absent-plan-dir>
kurn-search-fleet converge -plan <plan-dir> -state <absent-state-dir>
kurn-search-fleet inspect  -state <state-dir>
kurn-search-fleet recover  -spec <recovery.json> -state <state-dir> -out <absent-recovery-dir>
```

Inputs and private state may contain local paths. Successful stdout is one
canonical path-neutral result. Every failure is the stable `fleet: refused`
message. There is no discovery, interactive input, retry, redirect, route
application, deletion, retirement, remote access, credential creation, or
production control-plane behavior.

`ManagementTopology` truthfully records that the fixture probes share a
loopback listener; a separate production management listener remains unproved.
Role identifiers are deterministic conformance labels, not authentication.
Local backup and isolated restore copy the complete verified S3 artifact
inventory (bundle, manifest, optional delta, envelope, receipt, and record),
the path-neutral fleet evidence, and one exact three-file image per node. They
prove byte reconstruction and fixed ordered step counts only. They establish
no durable backup, DR, RPO/RTO, SLO, paging, TLS, IAM, capacity,
infrastructure, production fleet, routing, or customer-deployability property.
