# Plan: Add LineString, MultiLineString, MultiPoint to Dgraph Geo Support

## Context

Dgraph supports 3 of 7 GeoJSON geometry types: Point, Polygon, MultiPolygon. The remaining types — **LineString**, **MultiLineString**, **MultiPoint** — were requested in [dgraph-io/dgraph#2316](https://github.com/dgraph-io/dgraph/issues/2316) (April 2018), officially accepted (`status/accepted`, `priority/P2`), and lost in the July 2020 forum migration. GeometryCollection is a stretch goal only (rare in practice, architecturally different).

We develop to upstream-PR quality — a single complete feature covering DQL and GraphQL layers, tested end-to-end, that would be accepted on first submission if we chose to PR it.

**Key technical finding**: `s2.Polyline` implements `s2.Region` (verified: has `CapBound`, `RectBound`, `ContainsCell`, `IntersectsCell`, `CellUnionBound`). `RegionCoverer.Covering()` works directly on it. The covering strategy for LineString follows the same pattern as Polygon — no custom cell computation needed.

**Reference commits** (as code templates, not process templates):
- `17e96f736` — added MultiPolygon: single commit touching types, parser, query, docs. Predates Dgraph's GraphQL layer entirely. Best template for types-layer changes.
- `53822e8b6` — added GraphQL geo support (Polygon/MultiPolygon) as an independent later feature. Best template for GraphQL-layer changes.

## Architecture

Three layers, each type must pass through all of them:

```
Layer 1: Parsing & Storage    — GeoJSON/raw coords ↔ geom.T ↔ WKB binary
Layer 2: Indexing              — geom.T → S2 cell tokens (parent + cover)
Layer 3: Query Filtering       — S2 token lookup → exact geometry matching
```

Plus the GraphQL schema layer on top.

**Storage**: Single `GeoID` type for all geo subtypes. WKB binary format already handles all `geom.T` types transparently — no storage layer changes needed.

**Parser**: `dql/parser.go` (`parseGeoArgs`) already handles bracket depth up to 4. No parser changes needed.

## Semantics Matrix (Decision-Complete)

New types work as both **stored data** and **query arguments**. Query arguments require GeoJSON format (not raw bracket coordinates — see Bracket Ambiguity section).

### Stored data behavior

| Query function | Stored LineString | Stored MultiLineString | Stored MultiPoint |
|---|---|---|---|
| `near(point, dist)` | Match if line intersects near-loop | Match if any line intersects near-loop | Match if any point inside near-loop |
| `within(polygon)` | Match if entire line within query loop | Match if every line within some query loop | Match if every point within some query loop |
| `contains(point)` | Match if point lies on any segment | Match if point on any segment of any line | Match if any stored point equals query point |
| `contains(polygon)` | false (dimensional mismatch) | false | false |
| `intersects(polygon)` | Match if any segment crosses loop or any vertex inside loop | Match if any constituent line intersects | Match if any point inside loop |

### Query argument behavior

New types as arguments to geo functions, matched against stored data:

| Query function | LineString as argument | MultiPoint as argument |
|---|---|---|
| `intersects` | Polyline-vs-stored intersection | Each point tested against stored geometry |
| `contains` | Stored polygon must fully contain line | Stored polygon must contain all points |
| `within` | Not meaningful (lines have no area) — error | Not meaningful — error |

**Design rule**: Where a type combination doesn't make geometric sense, return `false` at the filter stage for stored data mismatches (consistent with existing behavior). Error only on malformed query arguments.

## Bracket Ambiguity Decision

The raw coordinate parser in `convertToGeom()` infers type from bracket depth:
- `[` → Point, `[[[` → Polygon, `[[[[` → MultiPolygon

LineString (`[[`), MultiPoint (`[[`), and MultiLineString (`[[[`) collide with existing patterns. **We do NOT modify the bracket parser.** New types require GeoJSON format:
```json
{"type": "LineString", "coordinates": [[lon, lat], [lon, lat], ...]}
```

This is correct because:
1. The GeoJSON path (`json.Unmarshal` at line 158 of `s2.go`) already handles all types automatically
2. Bracket-counting was always a convenience shortcut, not a standard
3. Adding heuristics to disambiguate `[[` would be fragile and break existing Point behavior
4. Stored data round-trips through WKB binary, so the bracket parser only matters for mutation input and query arguments

## Development Order

Build incrementally for development sanity, but the deliverable is one complete feature.

1. **Types layer: MultiPoint** — simplest, validates understanding of the pattern
2. **Types layer: LineString + MultiLineString** — new S2 primitive (Polyline)
3. **GraphQL layer: all three types** — schema, mutation/query rewriting, result serialization
4. **Integration tests + manual verification** — end-to-end DQL and GraphQL

---

## Types Layer: MultiPoint

MultiPoint is a collection of Points. Every operation decomposes to existing Point logic.

### `types/s2index.go` — `indexCells()` switch (line 67)

Add `case *geom.MultiPoint`: iterate points, index each, union cell coverages:
```go
case *geom.MultiPoint:
    var cover s2.CellUnion
    for i := range v.NumPoints() {
        p := v.Point(i)
        _, c := indexCellsForPoint(p, MinCellLevel, MaxCellLevel)
        cover = append(cover, c...)
    }
    parents := getParentCells(cover, MinCellLevel)
    return parents, cover, nil
```

### `types/s2.go` — `convertToGeom()` (line 124)

- GeoJSON path: already works — `geojson.Geometry.Decode()` returns `*geom.MultiPoint`
- Raw bracket path: no changes (ambiguous at `[[`)
- Add `*geom.MultiPoint` passthrough in `validate()` (no closed-loop check needed)

### `types/geofilter.go`

**Struct** — `GeoQueryData` (line 37): add fields for new types:
```go
type GeoQueryData struct {
    pt        *s2.Point
    pts       []s2.Point     // NEW: for MultiPoint queries
    loops     []*s2.Loop
    polylines []*s2.Polyline  // NEW: for LineString queries (Phase 2)
    qtype     QueryType
}
```

**`queryTokensGeo()`** (line 119): add `case *geom.MultiPoint` — convert each point to s2.Point, store in `pts`.

**`isWithin()`**: add `case *geom.MultiPoint` — all points must be within some query loop.

**`contains()`**: add `case *geom.MultiPoint` — if query is a point, match if any stored point equals it. If query is polygon, false.

**`intersects()`**: add `case *geom.MultiPoint` — any point inside any query loop.

---

## Types Layer: LineString + MultiLineString

### New helper functions in `types/s2index.go`

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

func coverPolyline(pl *s2.Polyline, minLevel, maxLevel, maxCells int) s2.CellUnion {
    rc := &s2.RegionCoverer{MinLevel: minLevel, MaxLevel: maxLevel, MaxCells: maxCells}
    return rc.Covering(pl)
}
```

### `types/s2index.go` — `indexCells()`

- `case *geom.LineString`: convert to Polyline, cover, get parents
- `case *geom.MultiLineString`: iterate component LineStrings, cover each, union results

### `types/s2.go` — `convertToGeom()`

- GeoJSON path: already works
- Add validation in `validate()`: LineString needs >= 2 coords, MultiLineString needs each component >= 2 coords

### `types/geofilter.go`

**`queryTokensGeo()`**: add cases — convert to polylines, compute covering, store in `polylines` field.

**`isWithin()`**: LineString within polygon = all vertices inside loop AND no edges cross boundary:
```go
func polylineWithinLoop(pl *s2.Polyline, l *s2.Loop) bool {
    for _, pt := range *pl {
        if !l.ContainsPoint(pt) {
            return false
        }
    }
    // All vertices inside — check no edges cross boundary
    for i := 0; i < pl.NumEdges(); i++ {
        edge := pl.Edge(i)
        for j := 0; j < l.NumEdges(); j++ {
            loopEdge := l.Edge(j)
            if s2.CrossingSign(edge.V0, edge.V1, loopEdge.V0, loopEdge.V1) == s2.Cross {
                return false
            }
        }
    }
    return true
}
```

**`contains(point)`**: Point-on-line check using `s2.Polyline.Project()` + distance threshold. `contains(polygon)` → false (dimensional mismatch).

**`intersects()`**: LineString vs Loop:
```go
func polylineIntersectsLoop(pl *s2.Polyline, l *s2.Loop) bool {
    // Any vertex inside loop → intersects
    for _, pt := range *pl {
        if l.ContainsPoint(pt) {
            return true
        }
    }
    // Any polyline edge crosses any loop edge → intersects
    for i := 0; i < pl.NumEdges(); i++ {
        edge := pl.Edge(i)
        for j := 0; j < l.NumEdges(); j++ {
            loopEdge := l.Edge(j)
            if s2.CrossingSign(edge.V0, edge.V1, loopEdge.V0, loopEdge.V1) == s2.Cross {
                return true
            }
        }
    }
    return false
}
```

Polyline-polyline: use `s2.Polyline.Intersects()` directly.

---

## GraphQL Layer (all three types)

### `graphql/schema/gqlschema.go`

**New constants** (~line 80):
```go
LineString      = "LineString"
MultiLineString = "MultiLineString"
MultiPoint      = "MultiPoint"
```

**New type definitions** (~line 194):
```graphql
type LineString { points: [Point!]! }
input LineStringRef { points: [PointRef!]! }
type MultiLineString { lines: [LineString!]! }
input MultiLineStringRef { lines: [LineStringRef!]! }
type MultiPoint { points: [Point!]! }
input MultiPointRef { points: [PointRef!]! }
```

**Filter types**: Reuse PolygonGeoFilter for new types (same operations supported).

**Map updates**: `supportedSearches`, `defaultSearches`, `builtInFilters`, `inbuiltTypeToDgraph` — add entries mapping new types to `"geo"`.

### `graphql/schema/rules.go`
- Update `isGeoType()` (line 1034): add LineString, MultiLineString, MultiPoint
- Update `preludeTypeNames` map (line 273): add new Ref and filter type names

### `graphql/schema/wrappers.go`
- Update `IsGeo()` method (line 2285)
- Update `isInputTypeGeo()` closure (line 561)

### `graphql/resolve/mutation_rewriter.go`
- Add `rewriteLineString()`, `rewriteMultiLineString()`, `rewriteMultiPoint()`
- Update `rewriteGeoObject()` switch (line 2079)

### `graphql/resolve/query_rewriter.go`
- Add `buildLineString()`, `buildMultiLineString()`, `buildMultiPoint()`
- Update filter cases for `contains` and `intersects` to accept new types

### `graphql/resolve/resolver.go`
- Add `completeLineString()`, `completeMultiLineString()`, `completeMultiPoint()` in `completeGeoObject()`

---

## Commit & PR Strategy

**One PR, structured commits.** The feature is incomplete without both DQL and GraphQL layers, and CONTRIBUTING.md explicitly says "Don't ship a half done feature." Clean commit boundaries give reviewers segmentation without splitting the delivery.

Following upstream conventions (Conventional Commits):

1. `feat(geo): add MultiPoint support for indexing and queries`
2. `feat(geo): add LineString and MultiLineString support for indexing and queries`
3. `feat(graphql): add LineString, MultiLineString, and MultiPoint geo types`
4. `test(geo): add integration tests for new geo types`
5. `docs: update geo type documentation for LineString, MultiLineString, MultiPoint`

Squash/reorganize before any upstream submission.

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

### Tier 3 — Integration (before deployment)
```bash
make test PKG=types
make test PKG=graphql/schema
make test PKG=graphql/resolve
make test PKG=query
```

### Manual verification
- Docker build via `Dockerfile.dev`
- `docker compose up` local cluster
- Mutate LineString/MultiLineString/MultiPoint data via DQL and GraphQL
- Query with `near`, `within`, `contains`, `intersects`
- Verify via Ratel visualization

## Acceptance Criteria

1. All existing geo tests pass unchanged
2. New tests cover every cell in the semantics matrix
3. No parser syntax changes
4. No index token format or storage format changes
5. Feature works end-to-end via both DQL and GraphQL
6. `trunk check` passes

## Key Files

| File | Changes |
|------|---------|
| `types/s2index.go` | indexCells cases + polylineFromLineString + coverPolyline |
| `types/s2.go` | convertToGeom validation for new types |
| `types/geofilter.go` | GeoQueryData struct + queryTokensGeo + all filter methods + helpers |
| `graphql/schema/gqlschema.go` | Type defs, constants, search/filter maps |
| `graphql/schema/rules.go` | isGeoType, preludeTypeNames |
| `graphql/schema/wrappers.go` | IsGeo, isInputTypeGeo |
| `graphql/resolve/mutation_rewriter.go` | rewrite functions + switch |
| `graphql/resolve/query_rewriter.go` | build functions + filter cases |
| `graphql/resolve/resolver.go` | complete functions in completeGeoObject |
