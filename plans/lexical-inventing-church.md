# Plan: Close Review Gaps and Make feat/s2-geo-new-types PR-Safe

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

### 1. Fix panic in query filter rewrite (`graphql/resolve/query_rewriter.go`)

Add length guards before `c[0], c[1]` access in `buildLineString`, `buildMultiLineString`,
and `buildMultiPoint`. On short/nil slices, emit a deterministic invalid coordinate
(e.g. `[0,0]`) that produces a normal geo parse error downstream — matching how existing
builders handle nil map values.

Files: `graphql/resolve/query_rewriter.go` (lines 2309-2355)

### 2. Add rewrite-layer panic regression tests

Add test cases in `graphql/resolve/query_test.yaml` (or Go test file) that:
- Build contains/intersects filters with malformed coordinates for each new type
- Assert no panic occurs
- Assert the rewritten DQL is well-formed (even if the geo arg is invalid)

Files: `graphql/resolve/query_test.yaml` or `graphql/resolve/query_rewriter_test.go`

### 3. Add GraphQL e2e coverage for new geo types

Extend the existing `Hotel` type in both e2e schemas with new geo fields:

```graphql
# Add to Hotel type in both schema files:
route: LineString @search
routes: MultiLineString @search
landmarks: MultiPoint @search
```

Add e2e test functions:
- Mutation round-trip for each new type (add + query back)
- Query with contains/intersects filters using new types
- Malformed coordinate filter that returns error without crashing

Files:
- `graphql/e2e/normal/schema.graphql`
- `graphql/e2e/directives/schema.graphql`
- `graphql/e2e/common/mutation.go` (mutation round-trips)
- `graphql/e2e/common/query.go` (filter queries)
- `graphql/e2e/common/common.go` (register new tests in RunAll)

### 4. Refresh e2e schema snapshots

Regenerate after schema changes:
- `graphql/e2e/normal/schema_response.json`
- `graphql/e2e/directives/schema_response.json`

Verify diffs are only the expected new geo field additions.

### 5. PR hygiene

- Split Dockerfile Go version bump (`bca2493d4`) into separate PR or squash out
- Decide whether `plans/` docs stay in the feature PR or get excluded
- Add PR description notes explaining:
  - Why `TestGeoFuncWithAfter` expected results changed (new test fixtures in near() range)
  - The panic fix and why it's needed despite GraphQL type validation

## Commit Sequence

1. `fix(graphql): guard geo filter rewrite against short coordinate arrays`
2. `test(graphql): add panic-regression tests for malformed geo filter coordinates`
3. `test(graphql): add e2e coverage for LineString/MultiLineString/MultiPoint`
4. `chore(graphql): refresh e2e schema snapshots for new geo types`

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
go test ./graphql/e2e/normal/... -tags=integration -run Geo -count=1
go test ./graphql/e2e/directives/... -tags=integration -run Geo -count=1

# Lint
trunk check
```
