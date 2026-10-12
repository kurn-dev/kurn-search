# TypeScript Development Compiler

The package and lockfile pin TypeScript 6.0.3. They are development tooling,
not runtime dependencies of the generated client. The npm package is Apache-2.0.

Install into a fresh compiler directory using `npm ci --ignore-scripts
--no-audit --no-fund`. Copy the package and lockfile there first. Do not install
the compiler into a prepared runtime version. SDK source installation can be
offline; acquiring this compiler may need the npm registry.
