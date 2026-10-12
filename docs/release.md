# Release Verification

This distribution contains the application-facing Search service, seven adapter
examples, the prepared-version helper, TypeScript and Python clients, and the
implementation libraries required to build those tools. Private
performance commands and the private historical evidence-generation system
are excluded from the supported distribution.

The release inventory identifies these selected source bytes. The coordinated
release handoff records checks executed against the candidate commit. The
OpenAPI and client `current-manifest.json` files, and the analyzer's
`semantic-identity.json`, are filtered public provenance summaries. They retain
public artifact hashes, upstream proof facts and the original receipt digest,
without private source inventories, file metadata or generator commands. The
original complete receipts remain private. These summaries do not attest this
selected public tree; its release inventory and runtime/client checks are
separate. Generated client headers and analyzer operational identities continue
to name the original upstream records, not the filtered summary bytes.

Run `node tools/check-publication.mjs` to validate the summary format, public
artifact hashes, source inventory and the preserved analyzer identity. The
public Git history contains only this filtered distribution; earlier candidate
history containing the original metadata is not a publication input.

The generated clients and MIT templates are included. The upstream evidence
generators are not a public code-generation command or supported API in this
distribution. Runtime construction and response validation do not require
them, a private repository, or registry installation.

Use a compatible engine containing prepared artifacts and exact companions.
The compatible engine is v0.7.0 at `3d6c938ab3d59bbd87bc697e5e25610569fbac4d`.
Use that exact source revision, or the v0.7.0 tag when available, rather than
assuming `go install @latest` selects it. Release preparation does not claim
that a repository, tag, registry package or hosted endpoint is already public.

The public release gate is builds and tests from this selected tree, the two
client test suites, prepared source-to-client queries, filters and no-match,
restart and version verification. Full host-failure, distributed deployment,
load qualification and cross-platform execution are separate claims. Compare
the final source inventory before publishing; do not reuse upstream private
proofs as evidence for changed public source bytes.

The public CI workflow runs formatting, build, vet, the complete Go suite and
race suite, both runtime client suites, publication/inventory checks and the
pinned TypeScript prepared-consumer checks. It uses only this public tree and
installs development tooling with package scripts disabled. It does not publish
packages, deploy services or access private source or evidence generators.
