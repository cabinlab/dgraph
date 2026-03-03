# S2 Geo Branch PR-Readiness Audit + Fixes

## Context

Branch `feat/s2-geo-new-types` adds LineString, MultiLineString, and MultiPoint support to Dgraph's geo system. A code review flagged potential bugs in the `contains` filter logic and gaps in test coverage. Exploration confirmed a real false-positive bug and identified missing negative test assertions in the integration suite. This plan fixes the bug, adds the missing tests, and separates unrelated Dockerfile changes — the minimum set to clear first-pass PR review.

## Branch state

- 19 commits on `feat/s2-geo-new-types`, 82 files changed
- Latest commit: `8bfa924c3` (refactor by team)
- Cluster running locally (image at `c08341d03`, **does not** include response completion fix or refactor — needs rebuild for GraphQL E2E)

---

## 1. Fix false-positive bug in `contains` (Stored Polygon case)

**File:** `types/geofilter.go:390-407`

**Bug:** When the query argument is a LineString or MultiPoint, `queryTokensGeo` populates `q.polylines` or `q.pts` respectively. But the stored Polygon case in `contains()` only checks `q.pt` (point) and `q.loops` (polygon/multipolygon). When neither is set, the `for _, l := range q.loops` loop iterates zero times and falls through to `return true` — a false positive.

The MultiPolygon case (line 408-429) correctly returns `false` for the same scenario.

**Correct semantics per the plan:**
- Stored Polygon + `contains(LineString)`: true iff the polygon fully contains the line
- Stored Polygon + `contains(MultiPoint)`: true iff the polygon contains every point
- Stored Polygon + `contains(MultiLineString)`: true iff the polygon fully contains every line
- Stored MultiPolygon needs the same treatment — it currently returns `false` but should implement real containment checks

**Fix for Stored Polygon (line 390-407):**

After the existing `q.loops` check, add:

```go
// Polygon contains LineString/MultiLineString: all segments must be inside.
if len(q.polylines) > 0 {
    for _, pl := range q.polylines {
        if !polylineWithinLoops(pl, []*s2.Loop{s2loop}) {
            return false
        }
    }
    return true
}

// Polygon contains MultiPoint: all points must be inside.
if len(q.pts) > 0 {
    for _, p := range q.pts {
        if !s2loop.ContainsPoint(p) {
            return false
        }
    }
    return true
}
```

Reuses existing `polylineWithinLoops` helper (line 621) and `s2.Loop.ContainsPoint`.

**Fix for Stored MultiPolygon (line 408-429):**

Before the `return false` at line 429, add equivalent logic: for polylines, check if each polyline is within at least one polygon's loop; for pts, check if each point is inside at least one polygon's loop.

```go
if len(q.polylines) > 0 {
    for _, pl := range q.polylines {
        if !polylineWithinMultiPolygonLoops(v, pl) {
            return false
        }
    }
    return true
}

if len(q.pts) > 0 {
    for _, p := range q.pts {
        if !multiPolygonContainsPoint(v, p) {
            return false
        }
    }
    return true
}
```

This needs two small helpers (`polylineWithinMultiPolygonLoops` and `multiPolygonContainsPoint`) that iterate over the MultiPolygon's component polygons — same pattern as `multiPolygonContainsLoop` (line 370).

---

## 2. Add unit tests for the fixed contains paths

**File:** `types/geofilter_test.go`

Add tests that would have caught the bug:

- `TestContainsPolygonWithLineStringQuery` — stored Polygon, query LineString fully inside → true; partially outside → false
- `TestContainsPolygonWithMultiPointQuery` — stored Polygon, query MultiPoint all inside → true; one point outside → false
- `TestContainsPolygonWithMultiLineStringQuery` — stored Polygon, query MultiLineString all inside → true; one line partially outside → false
- `TestContainsMultiPolygonWithLineStringQuery` — same pattern for stored MultiPolygon
- `TestContainsMultiPolygonWithMultiPointQuery` — same pattern for stored MultiPolygon

Each test must have both positive AND negative assertions using `require.True`/`require.False`.

---

## 3. Add negative assertions to integration tests

**File:** `query/query2_test.go`

All 11 new geo integration tests use only `require.Contains` (positive). Add `require.NotContains` or use `require.JSONEq` for exact matching where appropriate:

- `TestGeoWithinLineString`: assert that entities outside the query polygon are NOT returned
- `TestGeoContainsMultiPointArg`: add a test variant where one point is outside the polygon — should exclude that polygon from results
- `TestGeoExistingGeoUnchanged`: strengthen from `require.Contains` to `require.JSONEq` since this is a regression guard

Also add a new fixture entity in `query/common_test.go` that is deliberately far from the SF Bay test area (e.g., New York region) to serve as a universal negative control — should never appear in any SF-area geo query results.

---

## 4. Split Dockerfile changes into separate commit

**Current state:** Commit `16fb8f233` bundles a 6-line geo fix with 20 lines of Dockerfile changes (Go 1.25.0→1.25.7, apt version unpinning). This is PR noise.

**Action:** Interactive rebase to split `16fb8f233` into two commits:
- `fix(geo): update assertion guards for new query data fields` (geofilter.go only)
- `chore: bump Go to 1.25.7 and unpin apt versions in Dockerfile` (Dockerfile only)

---

## 5. Rebuild Docker image and re-run validation

After all fixes:

1. `go test ./types/... -run Contains -v -count=1` — verify new unit tests pass
2. `go test ./types/... -v -count=1` — full types suite (existing 82 tests + new)
3. Rebuild Docker image: `docker build -t dgraph/dgraph:local .` from dgraph dir
4. Restart alpha: `docker compose restart dgraph-alpha`
5. `go test ./query/... -tags=integration -run Geo -v -count=1` — all integration tests
6. Manual DQL smoke: `contains(geometry, MultiPoint)` and `contains(geometry, LineString)` against the live cluster — verify no false positives for polygons that don't actually contain the query geometry

---

## Files to modify

| File | Change |
|------|--------|
| `types/geofilter.go` | Fix contains() for Polygon and MultiPolygon + add 2 small helpers |
| `types/geofilter_test.go` | Add 5+ contains unit tests with positive and negative assertions |
| `query/query2_test.go` | Add negative assertions to existing tests, strengthen regression test |
| `query/common_test.go` | Add negative-control fixture entity far from SF test area |

## Files NOT modified (audit confirmed clean)

| File | Status |
|------|--------|
| `types/s2.go`, `types/s2index.go` | Parsing/indexing — no issues found |
| `graphql/schema/gqlschema.go` | Schema definitions correct |
| `graphql/resolve/mutation_rewriter.go` | Rewrite logic correct |
| `graphql/resolve/query_rewriter.go` | Query rewrite correct |
| `query/outputnode_graphql.go` | Response completion correct |
| `graphql/schema/rules.go`, `wrappers.go` | Clean after team refactor |
| 57 golden test files | Auto-regenerated, expected |
