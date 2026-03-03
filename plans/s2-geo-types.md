# Plan: Add LineString, MultiLineString, and MultiPoint to Dgraph Geo (S2) Support

## Implementation Status (updated 2026-03-02 session 2)

**Branch:** `feat/s2-geo-new-types`

### Completed
- [x] **Types layer** (commit `c802ac9fa`): s2.go, s2index.go, geofilter.go — parsing, indexing, filtering + 31 new unit tests (82 total pass)
- [x] **GraphQL layer** (commit `b837317eb`): gqlschema.go, rules.go, wrappers.go, mutation_rewriter.go, query_rewriter.go + 12 new test cases, 53 golden files regenerated
- [x] **DQL parser tests** (commit `2d29646db`): 4 parser lock-in tests (all pass), 12 query integration tests (compile, need cluster)
- [x] **Assertion fix** (commit `16fb8f23`): Fixed `isWithin()`/`contains()` AssertTruef guards that panicked when new query fields (polylines/pts) were set without loops
- [x] **Dockerfile** (commit `16fb8f23`): Bumped Go 1.25.0→1.25.7, unpinned apt versions
- [x] **GraphQL response completion** (uncommitted): Added `completeLineString`, `completeMultiLineString`, `completeMultiPoint`, and `writeCoordinateArray` to `query/outputnode_graphql.go`. The switch in `completeGeoObject` was missing cases for the 3 new types — they would have returned `"unsupported geo type"` at runtime. Fixed + compiles clean.
- [x] **Docker image rebuilt**: `dgraph/dgraph:local` rebuilt with all fixes. Image includes commit `c08341d03` (branch HEAD).
- [x] **Alpha restarted**: Container running on `dgraph-org_default` network, healthy (`/health` returns status=healthy, version=`v25.2.0-30-gc08341d03`). Assertion crash is resolved.
- [x] **Docs pass**: Audited all Go files for stale geo type lists. All comments in `wrappers.go`, `rules.go`, `mutation_rewriter.go`, `outputnode_graphql.go` already include the 6 types. No stale lists found.

### Remaining (next session)
1. **Commit** the `outputnode_graphql.go` fix (staged, not yet committed)
2. **Run query integration tests** against live cluster: `go test ./query/... -run Geo -v -count=1`
3. **Debug any runtime failures** from the integration tests
4. **End-to-end manual validation** — insert data via DQL/GraphQL, run all 4 geo functions, verify response shapes
5. **Final commit + plan update**

### Cluster State
- Zero: running (`dgraph-zero`)
- Alpha: running (`dgraph-alpha`), healthy, image `dgraph/dgraph:local` (rebuilt this session)
- Ratel: running (`dgraph-ratel`)
- Network: `dgraph-org_default`, alpha alias `alpha`
- Alpha data volume: `dgraph-org_dgraph-alpha-data` (may contain data from previous test runs)

### Known Issue (RESOLVED)
The `dgraph-alpha` container previously crashed with:
```
At least a point or loop should be defined.
types.GeoQueryData.contains (geofilter.go:386)
```
**Root cause:** The original `isWithin()` and `contains()` methods had `AssertTruef` guards that only checked `q.pt != nil || len(q.loops) > 0`. When new query types (polylines, pts) were used, the assertion fired. **Fix committed** in `16fb8f23` — assertions now include `q.polylines` and `q.pts`. Alpha now starts and stays healthy.

### Bug Found This Session
`query/outputnode_graphql.go:completeGeoObject` only handled Point/Polygon/MultiPolygon in its switch. LineString/MultiLineString/MultiPoint hit the `default` case returning `"unsupported geo type"`. This means GraphQL queries that return new geo types would fail at response serialization. Fixed by adding 3 new completion functions + a `writeCoordinateArray` helper. Compiles clean, builds clean.

---

## Intent
`17e96f7` is the floor, not the ceiling. For the new geo types, we will deliver an equivalent-or-better scope in current v25 architecture: types/indexing/filtering, parser implications, integration tests, and GraphQL surface support.

## Reference Baseline
1. Prior accepted request context: `dgraph-io/dgraph#2316`.
2. Precedent commits:
- `17e96f736`: MultiPolygon types-layer + query integration pattern.
- `53822e8b6`: MultiPolygon GraphQL-layer expansion pattern.

## Goal
Add first-class support for GeoJSON `LineString`, `MultiLineString`, and `MultiPoint` for `geo` predicates so they can be:
1. Stored and indexed.
2. Queried through DQL geo functions (`near`, `within`, `contains`, `intersects`).
3. Used through GraphQL built-in geo types and geo filters.

## Non-Goals
1. `GeometryCollection` support.
2. Changing geo token key format (`p/`, `c/`) or index constants.
3. Backward-incompatible DQL syntax changes.

## Architecture
Three layers plus GraphQL projection:
1. Parse/Storage layer:
- Input geometry parsing into `geom.T`.
- Storage remains unchanged: `GeoID` + WKB binary serialization/deserialization.
2. Indexing layer:
- `geom.T` to S2 parent/cover tokens.
3. Filter layer:
- Token prefilter + exact geometry check (`MatchGeo` path) with deterministic mismatch behavior.
4. GraphQL layer:
- Built-in geo type exposure + mutation/query rewriting + response completion.

No storage schema migration is required.

## Architecture and Compatibility Decisions
1. Keep existing bracket shorthand behavior unchanged:
- `[` => Point
- `[[[` => Polygon
- `[[[[` => MultiPolygon

2. Resolve bracket ambiguity by requiring GeoJSON object form for ambiguous new shapes in both mutation and query inputs:
- `LineString`, `MultiLineString`, `MultiPoint` must be supplied as GeoJSON objects (`{\"type\":...,\"coordinates\":...}`) where user-provided string geometry is involved.
- Existing shorthand arrays for Point/Polygon/MultiPolygon remain unchanged.

3. Maintain current dimensional constraints:
- `near` remains point + distance only.
- `within` remains area query (`Polygon`/`MultiPolygon`) only.

4. Reuse S2 primitives already available in dependency stack:
- `s2.Polyline` for line geometry covering and intersection checks.
- Existing `s2.Loop` logic for polygonal regions.
5. Explicit covering rationale:
- `s2.Polyline` satisfies the RegionCoverer contract needed for `RegionCoverer.Covering()`.
- No bespoke line-cell covering algorithm is introduced.

## Key Technical Finding
`s2.Polyline` already supports the region-covering path used by `s2.RegionCoverer.Covering()`. Therefore, line indexing can use the same covering strategy class as polygons without introducing a custom line-cover algorithm.

## Decision-Complete Semantics

### A. Stored geometry behavior (existing query arg forms + new GeoJSON query forms)
1. `near(point, distance)`:
- Stored `LineString`: true if line intersects near-cap loop.
- Stored `MultiLineString`: true if any line intersects near-cap loop.
- Stored `MultiPoint`: true if any point is inside near-cap loop.

2. `within(polygon|multipolygon)`:
- Stored `LineString`: true iff all segments are inside query region and no segment crosses out.
- Stored `MultiLineString`: true iff every component line satisfies within.
- Stored `MultiPoint`: true iff every point is inside query region.

3. `contains(queryGeom)`:
- Stored `LineString`:
  - contains(point): true if point lies on any segment.
  - contains(any non-point): false. *(Deliberate simplification: sub-segment overlap on a sphere with floating-point tolerance is intractable for practical purposes. Document in code comments.)*
- Stored `MultiLineString`:
  - contains(point): true if point lies on any segment of any line.
  - contains(any non-point): false. *(Same simplification as LineString.)*
- Stored `MultiPoint`:
  - contains(point): true if any stored point equals query point.
  - contains(any non-point): false.

4. `intersects(queryGeom)`:
- Stored `LineString`: true if any segment intersects query geometry or any relevant vertex inclusion check succeeds.
- Stored `MultiLineString`: true if any component line intersects.
- Stored `MultiPoint`: true if any point intersects query geometry (point-in-region or equality depending query type).

### B. Query argument behavior matrix

| Query function | Point arg | Polygon/MultiPolygon arg | LineString/MultiLineString arg | MultiPoint arg |
|---|---|---|---|---|
| `near` | valid | malformed query arg => error | malformed query arg => error | malformed query arg => error |
| `within` | malformed query arg => error | valid | malformed query arg => error | malformed query arg => error |
| `contains` | valid | valid | valid | valid |
| `intersects` | malformed query arg => error | valid | valid | valid |

### C. Mismatch policy
1. Malformed query argument shape/type for a function: return an error (query-time validation failure).
2. Geometrically invalid but well-formed type pairing during filter evaluation: return `false` (no match), not an error.

## Implementation Plan

## 1) Types Layer (`types/`)

### 1.1 `types/s2.go`
1. Extend `convertToGeom()` validation path for `LineString`, `MultiLineString`, and `MultiPoint`.
2. Keep current polygon closed-ring validation intact.
3. Add helper conversions:
- `polylineFromLineString(*geom.LineString) (*s2.Polyline, error)`
- `polylinesFromMultiLineString(*geom.MultiLineString) ([]*s2.Polyline, error)`
- `pointsFromMultiPoint(*geom.MultiPoint) ([]s2.Point, error)`

### 1.2 `types/s2index.go`
1. Extend `indexCells(g geom.T)` switch with:
- `*geom.LineString`
- `*geom.MultiLineString`
- `*geom.MultiPoint`
2. Covering strategy:
- `LineString`: `RegionCoverer.Covering(polyline)`.
- `MultiLineString`: union coverings of each polyline.
- `MultiPoint`: union point covers (`indexCellsForPoint`) for each point.
3. Add explicit helper parallel to `coverLoop` for implementation clarity:
- `coverPolyline(*s2.Polyline, minLevel, maxLevel, maxCells int) s2.CellUnion`
4. Parent derivation remains via `getParentCells`.

### 1.3 `types/geofilter.go`
1. Extend `GeoQueryData` to carry:
- query point (`pt`), point collections (`pts`), loops (`loops`), polylines (`polylines`).
2. Extend `queryTokensGeo()` to parse/build query representations for new geometry types.
3. Extend `isWithin`, `contains`, `intersects` switch handling for stored:
- `*geom.LineString`
- `*geom.MultiLineString`
- `*geom.MultiPoint`
4. Add focused helper ops:
- point-on-polyline with explicit tolerance
- polyline-within-loop
- polyline-intersects-loop
- multipoint-in-loops checks

5. Pre-decided helper algorithms:
- `point-on-polyline`:
  - Use `s2.DistanceFromSegment(queryPt, segA, segB)` per segment.
  - Define `lineContainsPointEpsilon = s1.Angle(1e-9)` and treat as on-line when distance <= epsilon.
- `polyline-within-loop`:
  - Every vertex must be inside target loop.
  - No polyline segment may cross any loop edge.
- `polyline-intersects-loop`:
  - True if any vertex is inside loop OR any segment crosses any loop edge.

### 1.4 `types/*_test.go`
1. `s2_test.go`: parse/validation for new GeoJSON types and invalid shape cases.
2. `s2index_test.go`: index coverage tests for all new types.
3. `geofilter_test.go`: function-by-function semantics tests for all new types.

## 2) DQL Layer (`dql/`, `query/`)

### 2.1 `dql/parser.go` + `dql/parser_test.go`
1. No parser behavior change is intended.
2. Add tests to lock intended behavior:
- Existing bracket shorthand unaffected.
- GeoJSON-object query arguments for new types parse correctly.
- Ambiguous shorthand arrays are not reinterpreted.
3. Only if implementation proves parser behavior blocks required valid input, add the minimal parser change with dedicated regression coverage.

### 2.2 `query/common_test.go`
1. Add fixture helpers:
- `addGeoLineStringToCluster`
- `addGeoMultiLineStringToCluster`
- `addGeoMultiPointToCluster`
2. Seed representative data into existing geo predicate fixtures.

### 2.3 `query/query1_test.go` / `query/query2_test.go`
1. Add end-to-end DQL tests for each function with each new stored type.
2. Include positive and negative cases, including edge and boundary behavior.
3. Add regression tests proving legacy point/polygon/multipolygon behavior remains unchanged.

## 3) GraphQL Layer (`graphql/schema`, `graphql/resolve`)

### 3.1 `graphql/schema/gqlschema.go`
1. Add built-in types and refs:
- `LineString`, `LineStringRef`
- `MultiLineString`, `MultiLineStringRef`
- `MultiPoint`, `MultiPointRef`
2. Wire search/index maps:
- `supportedSearches`
- `defaultSearches`
- `builtInFilters`
- `inbuiltTypeToDgraph`
3. Update geo constants used by resolvers/rewriters.

### 3.2 `graphql/schema/rules.go`, `graphql/schema/wrappers.go`
1. Update geo-type recognition (`IsGeo`, geo reserved handling).
2. Ensure generated schema and validation paths treat new geo types as inbuilt geo.

### 3.3 `graphql/resolve/mutation_rewriter.go`
1. Add coordinate rewrite functions for new geo input structures.
2. Extend geo rewrite dispatcher to output correct Dgraph JSON GeoJSON shapes.

### 3.4 `graphql/resolve/query_rewriter.go`
1. Add builders for new geo filter input objects.
2. Emit unambiguous DQL arguments for new query-argument geometries (GeoJSON object form where necessary).

### 3.5 `graphql/resolve/resolver.go`
1. Extend geo completion/serialization for response projection of new types.

### 3.6 GraphQL tests
1. Extend existing YAML suites:
- `graphql/resolve/add_mutation_test.yaml`
- `graphql/resolve/update_mutation_test.yaml`
- `graphql/resolve/query_test.yaml`
- relevant `graphql/schema` test fixtures.
2. Cover add/update/query round-trips for all three new geo types.

## 4) Documentation and Notes
1. Update in-repo geo function docs/comments where type lists are hardcoded.
2. If docs source is not in this repo, add clear code comments and release-note/changelog entry in repo conventions.

## Development Order
1. Types layer for `MultiPoint`.
2. Types layer for `LineString` and `MultiLineString`.
3. DQL parser/query test lock-in for ambiguity and regressions.
4. GraphQL schema/rewriter/resolver support for all three types.
5. End-to-end test sweep and doc updates.

## Commit Strategy
1. `feat(geo): add MultiPoint indexing and filtering support`
2. `feat(geo): add LineString and MultiLineString indexing and filtering support`
3. `feat(graphql): add LineString/MultiLineString/MultiPoint geo types and rewrites`
4. `test(geo): add DQL and GraphQL regression/integration coverage`
5. `docs(geo): update supported geometry type documentation`

## Verification Plan

### Tier 1: Fast correctness gates (on every iteration)
1. `go test ./types/...`
2. `go test ./dql/...`
3. `go test ./query/... -run Geo`
4. `go test ./graphql/schema/...`
5. `go test ./graphql/resolve/...`

### Tier 2: Targeted regressions
1. Existing geo tests unchanged pass rate.
2. Parser tests for geo arg forms pass.
3. GraphQL geo mutation/query fixtures pass.

### Tier 3: Full quality gate
1. `trunk check` (or project-standard lint/format/test target).
2. Full CI-equivalent local target if available.

### Manual validation
1. Insert representative data for each new type through DQL and GraphQL mutations.
2. Execute `near`, `within`, `contains`, `intersects` queries from both DQL and GraphQL.
3. Confirm serialized response shapes for new GraphQL built-ins.
4. Run local cluster via `docker compose up` and verify results interactively in Ratel.

## Acceptance Criteria
1. `LineString`, `MultiLineString`, `MultiPoint` are indexable on `geo` predicates.
2. All four geo functions work with defined semantics for new types.
3. No regression in existing Point/Polygon/MultiPolygon behavior.
4. GraphQL supports create/update/query of new geo types end-to-end.
5. No backward-incompatible parser behavior changes.
6. Tests cover unit + integration + GraphQL layers for new behavior.

## Key Files
1. `types/s2.go`
2. `types/s2index.go`
3. `types/geofilter.go`
4. `types/s2_test.go`
5. `types/s2index_test.go`
6. `types/geofilter_test.go`
7. `dql/parser.go`
8. `dql/parser_test.go`
9. `query/common_test.go`
10. `query/query1_test.go`
11. `query/query2_test.go`
12. `graphql/schema/gqlschema.go`
13. `graphql/schema/rules.go`
14. `graphql/schema/wrappers.go`
15. `graphql/resolve/mutation_rewriter.go`
16. `graphql/resolve/query_rewriter.go`
17. `graphql/resolve/resolver.go`
18. GraphQL schema/resolve test YAML files

## Assumptions
1. We target a single coherent feature implementation, not a reduced-scope placeholder.
2. Query-argument ambiguity is solved without breaking legacy shorthand syntax.
3. Any algorithmic tolerance constants for point-on-line checks will be fixed and documented in tests.
