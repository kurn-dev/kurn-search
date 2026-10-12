# Licensing boundaries

The Kurn Search service, adapters, Go tooling, examples, and other files in
this repository are licensed under AGPL-3.0-only; see [LICENSE](LICENSE).

The standalone TypeScript client under `clients/typescript/` and Python client
under `clients/python/` are licensed under MIT; each directory has its own
`LICENSE`. The matching client templates in `internal/clientgen/templates/`
are also offered under MIT so clients regenerated from those templates retain
the same grant. All other delivered Go code and validation tests are AGPL-3.0-only.

Kurn itself is a separate AGPL-3.0 project. Search communicates with `kurnd`
over its HTTP API. This repository contains no Kurn engine source.
