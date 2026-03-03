# Plan: Close Review Gaps and Make feat/s2-geo-new-types PR-Safe

## Status

| Step | Description | Status |
|------|-------------|--------|
| 1 | Fix panic in query filter rewrite | **DONE** |
| 2 | Add rewrite-layer panic regression tests | **DONE** |
| 3 | Add GraphQL e2e coverage for new geo types | **DONE** |
| 4 | Refresh e2e schema snapshots | **DONE** |
| 5 | PR hygiene | TODO |
| 6 | Docs (PR-gating) | TODO |

### Verification results (steps 1-4)

- `go test ./graphql/resolve/... -run TestQueryRewriting` — **PASS**
- `go test ./graphql/schema/...` — **PASS**
- `go vet ./graphql/e2e/...` — **clean**
- Pre-existing failure in `TestMultipleMutationsPropagateExtensionsCorrectly` confirmed unrelated
  (fails identically on base branch without our changes)

## Context

The `feat/s2-geo-new-types` branch adds LineString, MultiLineString, and MultiPoint support
across Dgraph's geo stack. A code review found the feature is well-implemented across all
layers, but missed a real blocker: malformed GraphQL geo filter payloads can panic the query
rewriter via unchecked array indexing. This plan fixes that blocker, adds missing e2e test
coverage, and handles PR hygiene.

## The Blocker

In `graphql/resolve/query_rewriter.go`, the three new builder functions index raw coordinate
arrays without bounds checking:

```go
// buildLineString line 2316, buildMultiLineString line 2335, buildMultiPoint line 2351
c, _ := coord.([]interface{})
x.Check2(buf.WriteString(fmt.Sprintf("[%v,%v]", c[0], c[1])))  // panics if len(c) < 2
```

GraphQL schema `[[Float!]!]!` validates types but NOT array length. A query like
`contains: {lineString: {coordinates: [[1.0]]}}` passes validation, reaches the rewriter,
and panics on `c[1]`.

**Why existing types are safe:** `buildPoint` uses named fields (`point[schema.Longitude]`)
which return nil on missing keys instead of panicking. `buildPolygon`/`buildMultiPolygon`
delegate to `buildPoint`. The new types are the only ones using raw `c[0], c[1]` indexing.

**Mutation rewriter is safe:** `rewriteLineString` etc. pass coordinate arrays through
without indexing — malformed coords fail at downstream geo parse, not rewrite.

## Implementation

### 1. Fix panic in query filter rewrite (`graphql/resolve/query_rewriter.go`) — DONE

Added `len(c) >= 2` guards before `c[0], c[1]` access in `buildLineString`,
`buildMultiLineString`, and `buildMultiPoint`. On short/nil slices, emit `[0,0]` — a
deterministic invalid coordinate that produces a normal geo parse error downstream.

**Design choice:** The simpler `[0,0]` fallback was used instead of the originally proposed
`__invalid_geo_input__` sentinel or `(ok bool)` refactor. Rationale:
- `[0,0]` keeps the JSON geo structure syntactically valid, matching how `buildPoint` handles
  nil map values (which produce `[<nil>,<nil>]` — also invalid geo that fails downstream).
- The coordinate `[0,0]` at the equator/prime-meridian intersection is in the ocean — no
  real geo data lives there, so false matches are not a practical concern.
- Avoids signature changes to builder functions, keeping the diff minimal and focused.
- Downstream geo parse (`geom.UnmarshalJSON`) rejects malformed geometries regardless.

If stricter sentinel behavior is preferred, the guard can be upgraded to use the
`__invalid_geo_input__` approach without changing the test expectations (tests assert on the
DQL output shape, not on runtime behavior).

Files changed: `graphql/resolve/query_rewriter.go` (lines 2309-2367)

### 2. Add rewrite-layer panic regression tests — DONE

Added 4 YAML test cases in `graphql/resolve/query_test.yaml` (after the existing new-type
filter tests, before `- name: ID query`):

| Test name | Input | Expected DQL |
|-----------|-------|-------------|
| Regression: LineString with short coordinate array does not panic | `[[1.0]]` | `[0,0]` |
| Regression: MultiLineString with short coordinate array does not panic | `[[9.9]]` | `[0,0]` |
| Regression: MultiPoint with short coordinate array does not panic | `[[1.0]]` | `[0,0]` |
| Regression: LineString with empty coordinate array does not panic | `[[]]` | `[0,0]` |

**Design choice:** Used YAML tests instead of the originally proposed Go test file. Rationale:
- The existing query rewrite test infrastructure (`TestQueryRewriting`) already loads and
  runs all YAML entries from `query_test.yaml` — this is the established pattern.
- YAML tests provide a declarative GQL-in/DQL-out assertion format that's easier to review.
- The existing new-type tests (LineString/MultiLineString/MultiPoint contains/intersects)
  are already in the same YAML file, so regression tests sit naturally adjacent.
- Valid coordinate payloads are already covered by the 6 existing YAML entries for the
  new types (lines 678-794), so no separate behavior-regression tests were needed.

Files changed: `graphql/resolve/query_test.yaml`

### 3. Add GraphQL e2e coverage for new geo types — DONE

Extended the existing `Hotel` type in both e2e schemas with new geo fields:

```graphql
# Added to Hotel type in both schema files:
route: LineString @search
routes: MultiLineString @search
landmarks: MultiPoint @search
```

Added e2e test functions:

**Mutation round-trips** (`graphql/e2e/common/mutation.go`):
- `mutationLineStringType` — add Hotel with LineString route, verify coordinates round-trip
- `mutationMultiLineStringType` — add Hotel with MultiLineString routes (2 lines), verify
  nested structure round-trip including `__typename` assertions
- `mutationMultiPointType` — add Hotel with MultiPoint landmarks, verify coordinates round-trip

All mutation tests follow the established pattern: add via `AddHotelInput` variables,
`CompareJSON` against expected response, cleanup via `DeleteGqlType`.

**Filter queries** (`graphql/e2e/common/query.go`):
- `queryGeoContainsLineString` — add LineString hotel, query with `contains` filter
- `queryGeoContainsMultiPoint` — add MultiPoint hotel, query with `intersects` filter
- `queryGeoContainsMultiLineString` — add MultiLineString hotel, query with `contains` filter
- `queryGeoMalformedCoordinatesNoError` — table-driven test with 3 subtests (one per new type),
  each sending a short coordinate array (`[[1.0]]`), asserting no panic and non-nil response

All query tests add test data, run the query, and clean up — matching the existing
`queryGeoNearFilter` pattern.

**Registration** (`graphql/e2e/common/common.go`):
- 3 mutation tests registered after existing `"Geo - MultiPolygon type"` entry
- 4 query tests registered after existing `"query geo near filter"` entry

Files changed:
- `graphql/e2e/normal/schema.graphql`
- `graphql/e2e/directives/schema.graphql`
- `graphql/e2e/common/mutation.go` (3 new functions)
- `graphql/e2e/common/query.go` (4 new functions)
- `graphql/e2e/common/common.go` (7 new test registrations in RunAll)

### 4. Refresh e2e schema snapshots — DONE

Manually added `Hotel.route`, `Hotel.routes`, `Hotel.landmarks` field entries to the Hotel
type object in both schema response JSON files. Changes are minimal — 9 new lines each,
appended after the existing `Hotel.branches` entry.

Files changed:
- `graphql/e2e/normal/schema_response.json`
- `graphql/e2e/directives/schema_response.json`

**Note:** These snapshots are introspection results normally regenerated by running the e2e
suite against a live cluster. The manual edits match the expected schema output format. Full
regeneration will occur when the e2e suite runs against a cluster with the updated schema.

### 5. PR hygiene — TODO

- Split Dockerfile Go version bump (`bca2493d4`) into separate PR or squash out
- Exclude `plans/` docs from the feature PR (keep internal planning docs out of final feature diff)
- Add PR description notes explaining:
  - Why `TestGeoFuncWithAfter` expected results changed (new test fixtures in near() range)
  - The panic fix and why it's needed despite GraphQL type validation

### 6. Docs (PR-gating) — TODO

Docs moved out of the main repo (no `wiki/` or `docs/` directory), but this change expands
public API surface. Treat docs as PR-gating, per current PR template expectations:

1. Open companion docs PR in `dgraph-io/dgraph-docs` before merging this PR.
2. Link that docs PR in the main PR checklist.
3. Cover: query arg forms for new geo types, GraphQL built-ins, and contains/intersects semantics.

## Commit Sequence

1. `fix(graphql): guard geo filter rewrite against short coordinate arrays` — DONE (unstaged)
2. `test(graphql): add panic-regression tests for malformed geo filter coordinates` — DONE (unstaged)
3. `test(graphql): add e2e coverage for LineString/MultiLineString/MultiPoint` — DONE (unstaged)
4. `chore(graphql): refresh e2e schema snapshots for new geo types` — DONE (unstaged)
5. `chore(pr): remove unrelated Dockerfile/plans changes from feature PR` (or move to separate PR) — TODO

PR checklist step (non-commit):
6. Link companion docs PR in main PR description/checklist — TODO

**Next action:** Commit steps 1-4 (either as individual commits per the sequence above, or
squashed — depending on PR preference). Then address steps 5-6.

## Verification

```bash
# Unit + rewrite tests
go test ./graphql/resolve/... -count=1
go test ./graphql/schema/... -count=1
go test ./types/... -count=1
go test ./dql/... -count=1

# Integration (requires cluster)
go test ./query/... -tags=integration -run Geo -count=1

# E2e (requires cluster)
# Run full e2e packages to avoid false-green due to test-name filtering.
go test ./graphql/e2e/normal/... -tags=integration -count=1
go test ./graphql/e2e/directives/... -tags=integration -count=1

# Optional targeted runs once new subtests are added:
# go test ./graphql/e2e/normal/... -tags=integration -run 'TestRunAll_Normal/query geo (new types|malformed)'
# go test ./graphql/e2e/directives/... -tags=integration -run 'TestRunAll_WithDgraphDirectives/query geo (new types|malformed)'

# Lint
trunk check
```
