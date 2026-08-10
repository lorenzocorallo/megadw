# Third-party notices

The release binary contains Go runtime dependencies and the production React
assets. The complete transitive dependency set is pinned in `go.sum` and
`web/package-lock.json`; `scripts/license-audit.sh` verifies that every Go
module has a discoverable license and every npm lockfile package has license
metadata. Optional yuku native binding packages omit the field from their tar
ball metadata; their MIT license is verified from the npm registry metadata and
listed explicitly in that audit script.

## Go runtime dependencies

| Module | Version | License |
| --- | --- | --- |
| `github.com/t3rm1n4l/go-mega` | `v0.0.0-20251120131202-6845944c051c` | MIT |
| `golang.org/x/crypto` | `v0.52.0` | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.56.0` | BSD-3-Clause |

The indirect Go modules are retained at the exact versions in `go.mod` and
`go.sum`; their upstream license files are checked by the release audit.

## Frontend direct dependencies

The direct frontend dependencies are all permissively licensed: MIT except
`@fontsource-variable/geist` (OFL-1.1), `class-variance-authority` and
`@playwright/test`/`typescript` (Apache-2.0), and `lucide-react` (ISC). Their
exact versions and the full transitive record are in `web/package-lock.json`.

The build-only Vite+ and TypeScript toolchain is not required by the runtime
binary. Upstream license text remains in the installed source distribution;
release verification is performed before packaging.
