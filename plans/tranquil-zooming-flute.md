# Plan: Add Missing GeoJSON Types to Dgraph

## Context

Dgraph supports 3 of 7 GeoJSON geometry types: Point, Polygon, MultiPolygon. The remaining types — **LineString**, **MultiLineString**, **MultiPoint** — were requested in [dgraph-io/dgraph#2316](https://github.com/dgraph-io/dgraph/issues/2316) (April 2018), officially accepted (`status/accepted`, `priority/P2`), and lost in the July 2020 forum migration. GeometryCollection is out of scope (rare in practice, architecturally different).

We need LineString/MultiLineString for our own projects. We develop at upstream-PR quality so we can contribute back when ready.

**Key technical finding**: `s2.Polyline` implements `s2.Region` (has `CapBound`, `RectBound`, `ContainsCell`, `IntersectsCell`, `CellUnionBound`), so `RegionCoverer.Covering()` works directly on it. This means the covering strategy for LineString follows the exact same pattern as Polygon — no custom cell computation needed.

**Reference commits**:
- `17e96f736` — added MultiPolygon to types layer (our primary template)
- `53822e8b6` — added MultiPolygon to GraphQL layer

## Architecture

Three layers, each type must pass through all of them:

```
Layer 1: Parsing & Storage    — GeoJSON/raw coords ↔ geom.T ↔ WKB binary
Layer 2: Indexing              — geom.T → S2 cell tokens (parent + cover)
Layer 3: Query Filtering       — S2 token lookup → exact geometry matching
```

Plus the GraphQL schema layer on top.

**Storage**: Single `GeoID` type for all geo subtypes. WKB binary format already handles all geom.T types transparently — no storage changes needed.

## Implementation Order

**Phase 1: MultiPoint** (simplest — validates our understanding of the pattern)
**Phase 2: LineString + MultiLineString** (the real feature — new S2 primitive)
**Phase 3: GraphQL layer for all three types**

Each phase produces a working, testable increment.

---

## Phase 1: MultiPoint

MultiPoint is a collection of Points. Every operation decomposes to existing Point logic.

### Files to modify

**`types/s2index.go`** — `indexCells()` switch (line 67)
- Add `case *geom.MultiPoint`: iterate points, collect cell unions (same pattern as MultiPolygon iterating polygons)

**`types/s2.go`** — `convertToGeom()` (line 124)
- GeoJSON path (line 158): already works — `geojson.Geometry.Decode()` returns `*geom.MultiPoint` automatically
- Raw coordinate path: MultiPoint is `[[lon,lat],[lon,lat]]` — 2 brackets, same as a coordinate array. Cannot be disambiguated from LineString by bracket depth alone. **Decision: require GeoJSON format for MultiPoint raw input. Do not add bracket-counting for ambiguous types.** The existing `[` → Point fallback is preserved.
- Add `*geom.MultiPoint` to the `validate()` function (no closed-loop validation needed for points)

**`types/geofilter.go`** — multiple functions:
- `queryTokensGeo()` (line 119): add `case *geom.MultiPoint` — convert each point to s2.Point, store in a new `pts []*s2.Point` field or reuse existing `pt` with slice
- `GeoQueryData` struct (line 37): extend with `pts []s2.Point` for MultiPoint queries
- `isWithin()` (line 236): add `case *geom.MultiPoint` — all points must be within query loops
- `contains()` (line 300): add `case *geom.MultiPoint` — stored MultiPoint contains query if it has matching points (or: stored polygon contains all points of query MultiPoint)
- `intersects()` (line 363): add `case *geom.MultiPoint` — any point intersects query loops

### Tests

- Unit tests in `types/geofilter_test.go` and `types/s2index_test.go`
- Test data: GeoJSON MultiPoint fixtures
- Verify: index, within, contains, intersects, near queries

---

## Phase 2: LineString + MultiLineString

### New helper functions needed

**`types/s2index.go`**:

```go
func polylineFromLineString(ls *geom.LineString) (*s2.Polyline, error) {
    n := ls.NumCoords()
    if n < 2 {
        return nil, errors.Errorf("LineString requires at least 2 points")
    }
    pts := make(s2.Polyline, n)
    for i := range n {
        pts[i] = pointFromCoord(ls.Coord(i))
    }
    return &pts, nil
}
```

**Covering strategy**: Use `coverPolyline()` — same structure as `coverLoop()`:
```go
func coverPolyline(pl *s2.Polyline, minLevel, maxLevel, maxCells int) s2.CellUnion {
    rc := &s2.RegionCoverer{MinLevel: minLevel, MaxLevel: maxLevel, MaxCells: maxCells}
    return rc.Covering(pl)
}
```

This works because `s2.Polyline` implements `s2.Region`.

### Files to modify

**`types/s2index.go`** — `indexCells()`:
- Add `case *geom.LineString`: convert to s2.Polyline, cover, get parents
- Add `case *geom.MultiLineString`: iterate LineStrings, cover each, union results

**`types/s2.go`** — `convertToGeom()`:
- GeoJSON path: already works automatically
- Raw coordinate path: LineString is `[[lon,lat],[lon,lat]]` — 2 brackets. Same ambiguity as MultiPoint. **Decision: same as MultiPoint — require GeoJSON format for new types. No bracket-counting additions.**
- Add `*geom.LineString` validation (at least 2 coords)
- Add `*geom.MultiLineString` validation (each component at least 2 coords)

**`types/geofilter.go`**:
- `GeoQueryData` struct: add `polylines []*s2.Polyline`
- `queryTokensGeo()`: add cases for LineString and MultiLineString — convert to polylines, compute covering
- `isWithin()`: LineString is within a polygon if all vertices are inside and no edges cross the boundary
- `contains()`: A polygon contains a LineString if the LineString is within it. A LineString "contains" a point only if the point lies on the line (use `s2.Polyline.Project()` + distance check).
- `intersects()`: Use `s2.Polyline.Intersects()` for polyline-polyline. For polyline-loop intersection, check if any polyline edge crosses any loop edge, or if any polyline vertex is inside the loop.

### Intersection logic detail

For LineString vs Loop intersection, we need a helper:
```go
func polylineIntersectsLoop(pl *s2.Polyline, l *s2.Loop) bool {
    // Check if any polyline vertex is inside the loop
    for _, pt := range *pl {
        if l.ContainsPoint(pt) {
            return true
        }
    }
    // Check if any polyline edge crosses any loop edge
    // Use s2.CrossingSign for edge pairs
    ...
}
```

### Query operation semantics for LineString

| Query | LineString as stored data | LineString as query input |
|-------|-------------------------|-------------------------|
| near | Any point on line within distance of query point | N/A (near takes a point) |
| within | Entire line within query polygon | N/A (within takes a polygon) |
| contains | Line contains query point (point-on-line check) | Polygon contains entire line |
| intersects | Line intersects query polygon/line | Query polygon/line intersects stored data |

---

## Phase 3: GraphQL Layer

### `graphql/schema/gqlschema.go`

**New constants** (line ~80):
```go
LineString      = "LineString"
MultiLineString = "MultiLineString"
MultiPoint      = "MultiPoint"
```

**New type definitions** (line ~194):
```graphql
type LineString { points: [Point!]! }
input LineStringRef { points: [PointRef!]! }
type MultiLineString { lines: [LineString!]! }
input MultiLineStringRef { lines: [LineStringRef!]! }
type MultiPoint { points: [Point!]! }
input MultiPointRef { points: [PointRef!]! }
```

**New filter types**: Reuse PolygonGeoFilter for LineString types (same operations).

**Map updates**: `supportedSearches`, `defaultSearches`, `builtInFilters`, `inbuiltTypeToDgraph` — add entries mapping new types to `"geo"`.

### `graphql/schema/rules.go`
- Update `isGeoType()` (line 1034)
- Update `preludeTypeNames` map (line 273)

### `graphql/schema/wrappers.go`
- Update `IsGeo()` method (line 2285)
- Update `isInputTypeGeo()` closure (line 561)

### `graphql/resolve/mutation_rewriter.go`
- Add `rewriteLineString()`, `rewriteMultiLineString()`, `rewriteMultiPoint()` — convert GraphQL input to GeoJSON coordinate arrays
- Update `rewriteGeoObject()` switch (line 2079)

### `graphql/resolve/query_rewriter.go`
- Add `buildLineString()`, `buildMultiLineString()`, `buildMultiPoint()` — serialize to DQL format
- Update filter cases for `contains` and `intersects` to accept new types

### `graphql/resolve/resolver.go`
- Add `completeLineString()`, `completeMultiLineString()`, `completeMultiPoint()` in `completeGeoObject()`

---

## Bracket Ambiguity Decision

The raw coordinate parser in `convertToGeom()` infers type from bracket depth:
- `[` → Point, `[[[` → Polygon, `[[[[` → MultiPolygon

LineString (`[[`), MultiPoint (`[[`), and MultiLineString (`[[[`) collide with existing patterns. **We do NOT modify the bracket parser.** New types require GeoJSON format:
```json
{"type": "LineString", "coordinates": [[lon, lat], [lon, lat], ...]}
```

This is the correct approach because:
1. The GeoJSON path (`json.Unmarshal`) already handles all types automatically
2. Bracket-counting was always a convenience shortcut, not a standard
3. Adding heuristics to disambiguate `[[` would be fragile and break existing Point behavior

---

## Commit Strategy

Following upstream conventions (Conventional Commits):

1. `feat(geo): add MultiPoint support for indexing and queries`
2. `feat(geo): add LineString and MultiLineString support for indexing and queries`
3. `feat(graphql): add LineString, MultiLineString, and MultiPoint geo types`
4. `test(geo): add integration tests for new geo types`
5. `docs: update geo type documentation for LineString, MultiLineString, MultiPoint`

Squash/reorganize before PR as needed.

---

## Verification

### Tier 1 — Compile + lint (every edit)
```bash
go build ./types/... ./graphql/...
go vet ./types/... ./graphql/...
trunk check
```

### Tier 2 — Unit tests (before pushing)
```bash
go test ./types/... -run TestGeo -v
go test ./graphql/schema/... -v
go test ./graphql/resolve/... -v
```

### Tier 3 — Integration (before PR)
```bash
make test PKG=types
make test PKG=graphql/schema
make test PKG=graphql/resolve
make test PKG=query
```

### Manual verification
- Docker build via `Dockerfile.dev`
- `docker compose up` local cluster
- Mutate LineString/MultiLineString/MultiPoint data via DQL
- Query with `near`, `within`, `contains`, `intersects`
- Verify via Ratel visualization

---

## Key Files Summary

| File | Changes |
|------|---------|
| `types/s2index.go` | indexCells cases + polylineFromLineString + coverPolyline |
| `types/s2.go` | convertToGeom validation for new types |
| `types/geofilter.go` | GeoQueryData struct + queryTokensGeo + all filter methods |
| `graphql/schema/gqlschema.go` | Type defs, constants, search/filter maps |
| `graphql/schema/rules.go` | isGeoType, preludeTypeNames |
| `graphql/schema/wrappers.go` | IsGeo, isInputTypeGeo |
| `graphql/resolve/mutation_rewriter.go` | rewrite functions + switch |
| `graphql/resolve/query_rewriter.go` | build functions + filter cases |
| `graphql/resolve/resolver.go` | complete functions in completeGeoObject |
