# S2 Geo Branch PR-Readiness Audit + Fixes

## Context

Branch `feat/s2-geo-new-types` adds LineString, MultiLineString, and MultiPoint support to Dgraph's geo system. A code review flagged potential bugs in the `contains` filter logic and gaps in test coverage. Exploration confirmed a real false-positive bug and identified missing negative test assertions in the integration suite. This plan fixes the bug, adds the missing tests, and separates unrelated Dockerfile changes — the minimum set to clear first-pass PR review.

## Branch state

- 20 commits on `feat/s2-geo-new-types`, 82 files changed
- Latest commit: `9ffa3ce57` (plan file)
- Cluster running locally (image at `c08341d03`, **does not** include response completion fix or refactor — needs rebuild for E2E)

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

Before the `return false` at line 429, add equivalent logic.

**Semantic decision: per-component containment, not union.** A MultiPolygon "contains" a geometry if the geometry is contained by at least one component polygon — not by the union of all polygons. This means a LineString that spans from polygon A to polygon B (crossing a gap between them) is NOT contained. This is the same semantic model already used by `isWithin` (`loopWithinMultiloops` at line 273) and `polylineWithinLoops` (line 621). Union-based containment would require constructing a merged polygon, which S2 doesn't support natively and is out of scope.

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

Needs two small helpers (`polylineWithinMultiPolygonLoops` and `multiPolygonContainsPoint`) that iterate over component polygons — same pattern as `multiPolygonContainsLoop` (line 370).

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

Use the existing NY-area MultiPolygon fixture (UID 5107, `common_test.go:1005`) as the negative control — it's already far from the SF Bay test area. Add `require.NotContains` for it (or its name) in relevant tests. No new fixture needed.

---

## 4. Split Dockerfile changes into separate commit

**Current state:** Commit `16fb8f233` bundles a 6-line geo fix with 20 lines of Dockerfile changes (Go 1.25.0→1.25.7, apt version unpinning). This is PR noise.

**Action:** Deterministic non-interactive split using `git rebase` with a sequence of `git reset HEAD~1 --soft`, selective staging, and two new commits. No interactive editor needed:
1. Start rebase: `git rebase --onto <parent-of-16fb8f23> <parent-of-16fb8f23> feat/s2-geo-new-types` (or `git rebase <parent>`)
2. At the target commit, `git reset HEAD~1` to unstage
3. `git add types/geofilter.go` → commit `fix(geo): update assertion guards for new query data fields`
4. `git add Dockerfile` → commit `chore: bump Go to 1.25.7 and unpin apt versions in Dockerfile`
5. `git rebase --continue`

Alternatively, if the rebase proves fiddly with this many downstream commits, just leave it — it's cosmetic noise, not a functional issue. The geo fix and Dockerfile change are both independently correct.

---

## 5. Rebuild Docker image and re-run validation

After all fixes:

1. `go test ./types/... -run Contains -v -count=1` — verify new unit tests pass
2. `go test ./types/... -v -count=1` — full types suite (existing 82 tests + new)
3. Rebuild Docker image and recreate container (from repo root, not dgraph subdir):
   ```
   docker compose up -d --build --force-recreate dgraph-alpha
   ```
   **Note:** `docker compose restart` reuses the old image — `--build --force-recreate` is required to pick up code changes.
4. Wait for alpha health: `curl http://localhost:8080/health` until status=healthy
5. `go test ./query/... -tags=integration -run Geo -v -count=1` — all integration tests
6. Manual DQL smoke: `contains(geometry, MultiPoint)` and `contains(geometry, LineString)` against the live cluster — verify no false positives for polygons that don't actually contain the query geometry

---

## Files to modify

| File | Change |
|------|--------|
| `types/geofilter.go` | Fix contains() for Polygon and MultiPolygon + add 2 small helpers |
| `types/geofilter_test.go` | Add 5+ contains unit tests with positive and negative assertions |
| `query/query2_test.go` | Add negative assertions to existing tests using UID 5107 (NY fixture), strengthen regression test |

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
