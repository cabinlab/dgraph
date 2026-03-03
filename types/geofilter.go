/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package types

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/golang/geo/s1"
	"github.com/golang/geo/s2"
	"github.com/pkg/errors"
	geom "github.com/twpayne/go-geom"
	"github.com/twpayne/go-geom/xy"

	"github.com/dgraph-io/dgraph/v25/protos/pb"
	"github.com/dgraph-io/dgraph/v25/x"
)

// QueryType indicates the type of geo query.
type QueryType byte

const (
	// QueryTypeWithin finds all points that are within the given geometry
	QueryTypeWithin QueryType = iota
	// QueryTypeContains finds all polygons that contain the given point
	QueryTypeContains
	// QueryTypeIntersects finds all objects that intersect the given geometry
	QueryTypeIntersects
	// QueryTypeNear finds all points that are within the given distance from the given point.
	QueryTypeNear
)

// GeoQueryData is pb.data used by the geo query filter to additionally filter the geometries.
type GeoQueryData struct {
	pt        *s2.Point      // If not nil, the input data was a point
	pts       []s2.Point     // For MultiPoint query args
	loops     []*s2.Loop     // If not empty, the input data was a polygon/multipolygon or it was a near query.
	polylines []*s2.Polyline // For LineString/MultiLineString query args
	qtype     QueryType
}

// IsGeoFunc returns if a function is of geo type.
func IsGeoFunc(str string) bool {
	switch str {
	case "near", "contains", "within", "intersects":
		return true
	}

	return false
}

// GetGeoTokens returns the corresponding index keys based on the type
// of function.
func GetGeoTokens(srcFunc *pb.SrcFunction) ([]string, *GeoQueryData, error) {
	x.AssertTruef(len(srcFunc.Name) > 0, "Invalid function")
	funcName := strings.ToLower(srcFunc.Name)
	switch funcName {
	case "near":
		if len(srcFunc.Args) != 2 {
			return nil, nil, errors.Errorf("near function requires 2 arguments, but got %d",
				len(srcFunc.Args))
		}
		maxDist, err := strconv.ParseFloat(srcFunc.Args[1], 64)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "Error while converting distance to float")
		}
		if maxDist < 0 {
			return nil, nil, errors.Errorf("Distance cannot be negative")
		}
		g, err := convertToGeom(srcFunc.Args[0])
		if err != nil {
			return nil, nil, err
		}
		return queryTokensGeo(QueryTypeNear, g, maxDist)
	case "within":
		if len(srcFunc.Args) != 1 {
			return nil, nil, errors.Errorf("within function requires 1 arguments, but got %d",
				len(srcFunc.Args))
		}
		g, err := convertToGeom(srcFunc.Args[0])
		if err != nil {
			return nil, nil, err
		}
		return queryTokensGeo(QueryTypeWithin, g, 0.0)
	case "contains":
		if len(srcFunc.Args) != 1 {
			return nil, nil, errors.Errorf("contains function requires 1 arguments, but got %d",
				len(srcFunc.Args))
		}
		g, err := convertToGeom(srcFunc.Args[0])
		if err != nil {
			return nil, nil, err
		}
		return queryTokensGeo(QueryTypeContains, g, 0.0)
	case "intersects":
		if len(srcFunc.Args) != 1 {
			return nil, nil, errors.Errorf("intersects function requires 1 arguments, but got %d",
				len(srcFunc.Args))
		}
		g, err := convertToGeom(srcFunc.Args[0])
		if err != nil {
			return nil, nil, err
		}
		return queryTokensGeo(QueryTypeIntersects, g, 0.0)
	default:
		return nil, nil, errors.Errorf("Invalid geo function")
	}
}

// queryTokensGeo returns the tokens to be used to look up the geo index for a given filter.
// qt is the type of Geo query - near/intersects/contains/within
// g is the geom.T representation of the input. It could be a point/polygon/multipolygon.
// maxDistance is distance in metres, only used for near query.
func queryTokensGeo(qt QueryType, g geom.T, maxDistance float64) ([]string, *GeoQueryData, error) {
	var loops []*s2.Loop
	var polylines []*s2.Polyline
	var pts []s2.Point
	var pt *s2.Point
	var err error
	switch v := g.(type) {
	case *geom.Point:
		// Get s2 point from geom.Point.
		p := pointFromPoint(v)
		pt = &p

		if qt == QueryTypeNear {
			// We use the point and make a loop with radius maxDistance. Then we can use this for
			// the rest of the query.
			if maxDistance <= 0 {
				return nil, nil, errors.Errorf("Invalid max distance specified for a near query")
			}
			a := EarthAngle(maxDistance)
			l := s2.RegularLoop(*pt, a, 100)
			loops = append(loops, l)
		}
	case *geom.Polygon:
		l, err := loopFromPolygon(v)
		if err != nil {
			return nil, nil, err
		}
		loops = append(loops, l)

	case *geom.MultiPolygon:
		// We get a loop for each polygon.
		for i := range v.NumPolygons() {
			l, err := loopFromPolygon(v.Polygon(i))
			if err != nil {
				return nil, nil, err
			}
			loops = append(loops, l)
		}

	case *geom.LineString:
		if qt == QueryTypeWithin || qt == QueryTypeNear {
			return nil, nil, errors.Errorf("Cannot use LineString as argument for %v query", qt)
		}
		pl, err := polylineFromLineString(v)
		if err != nil {
			return nil, nil, err
		}
		polylines = append(polylines, pl)

	case *geom.MultiLineString:
		if qt == QueryTypeWithin || qt == QueryTypeNear {
			return nil, nil, errors.Errorf("Cannot use MultiLineString as argument for %v query", qt)
		}
		pls, err := polylinesFromMultiLineString(v)
		if err != nil {
			return nil, nil, err
		}
		polylines = append(polylines, pls...)

	case *geom.MultiPoint:
		if qt == QueryTypeWithin || qt == QueryTypeNear {
			return nil, nil, errors.Errorf("Cannot use MultiPoint as argument for %v query", qt)
		}
		p, err := pointsFromMultiPoint(v)
		if err != nil {
			return nil, nil, err
		}
		pts = p

	default:
		return nil, nil, errors.Errorf("Cannot query using a geometry of type %T", v)
	}

	x.AssertTruef(len(loops) > 0 || pt != nil || len(polylines) > 0 || len(pts) > 0,
		"We should have a point, loop, polyline, or points.")

	var cover, parents s2.CellUnion
	if qt == QueryTypeNear {
		if len(loops) == 0 {
			return nil, nil, errors.Errorf("Internal error while processing near query.")
		}
		cover = coverLoop(loops[0], MinCellLevel, MaxCellLevel, MaxCells)
		parents = getParentCells(cover, MinCellLevel)
	} else {
		parents, cover, err = indexCells(g)
		if err != nil {
			return nil, nil, err
		}
	}

	qd := &GeoQueryData{
		pt:        pt,
		pts:       pts,
		loops:     loops,
		polylines: polylines,
		qtype:     qt,
	}

	switch qt {
	case QueryTypeWithin:
		// For a within query we only need to look at the objects whose parents match our cover.
		// So we take our cover and prefix with the parentPrefix to look in the index.
		if len(loops) == 0 {
			return nil, nil, errors.Errorf("Require a polygon for within query")
		}
		toks := createTokens(cover, parentPrefix)
		return toks, qd, nil

	case QueryTypeContains:
		// For a contains query, we only need to look at the objects whose cover matches our
		// parents. So we take our parents and prefix with the coverPrefix to look in the index.
		return createTokens(parents, coverPrefix), qd, nil

	case QueryTypeNear:
		if pt == nil {
			return []string{}, nil, errors.Errorf("Require a point for a within query.")
		}
		// A near query is the same as the intersects query. We form a loop with the given point and
		// the radius and then see what all does it intersect with. The point itself is not needed
		// for the intersects filter — only the loop matters.
		toks := parentCoverTokens(parents, cover)
		qd.pt = nil
		qd.qtype = QueryTypeIntersects
		return toks, qd, nil

	case QueryTypeIntersects:
		// An intersects query is as the name suggests all the entities which intersect with the
		// given region. So we look at all the objects whose parents match our cover as well as
		// all the objects whose cover matches our parents.
		if len(loops) == 0 && len(polylines) == 0 && len(pts) == 0 {
			return nil, nil, errors.Errorf("Require a polygon, line, or points for intersects query")
		}
		toks := parentCoverTokens(parents, cover)
		return toks, qd, nil

	default:
		return nil, nil, errors.Errorf("Unknown query type")
	}
}

// MatchesFilter applies the query filter to a geo value
func (q GeoQueryData) MatchesFilter(g geom.T) bool {
	switch q.qtype {
	case QueryTypeWithin:
		return q.isWithin(g)
	case QueryTypeContains:
		return q.contains(g)
	case QueryTypeIntersects:
		return q.intersects(g)
	case QueryTypeNear:
		return q.intersects(g)
	}
	return false
}

func loopWithinMultiloops(l *s2.Loop, loops []*s2.Loop) bool {
	for _, s2loop := range loops {
		if Contains(s2loop, l) {
			return true
		}
	}
	return false
}

// returns true if the geometry represented by g is within the given loop
func (q GeoQueryData) isWithin(g geom.T) bool {
	x.AssertTruef(q.pt != nil || len(q.loops) > 0 || len(q.polylines) > 0 || len(q.pts) > 0,
		"At least a point, loop, polyline, or point set should be defined.")
	switch geometry := g.(type) {
	case *geom.Point:
		s2pt := pointFromPoint(geometry)
		if q.pt != nil {
			return q.pt.ApproxEqual(s2pt)
		}

		if len(q.loops) > 0 {
			for _, l := range q.loops {
				if l.ContainsPoint(s2pt) {
					return true
				}
			}
			return false
		}
	case *geom.Polygon:
		s2loop, err := loopFromPolygon(geometry)
		if err != nil {
			return false
		}
		if len(q.loops) > 0 {
			for _, l := range q.loops {
				if Contains(l, s2loop) {
					return true
				}
			}
			return false
		}
	case *geom.MultiPolygon:
		// We check each polygon in the multipolygon should be within some loop of q.loops.
		if len(q.loops) > 0 {
			for i := range geometry.NumPolygons() {
				s2loop, err := loopFromPolygon(geometry.Polygon(i))
				if err != nil {
					return false
				}
				if !loopWithinMultiloops(s2loop, q.loops) {
					return false
				}
			}
			return true
		}
	case *geom.LineString:
		if len(q.loops) > 0 {
			pl, err := polylineFromLineString(geometry)
			if err != nil {
				return false
			}
			return polylineWithinLoops(pl, q.loops)
		}
	case *geom.MultiLineString:
		if len(q.loops) > 0 {
			for i := 0; i < geometry.NumLineStrings(); i++ {
				pl, err := polylineFromLineString(geometry.LineString(i))
				if err != nil {
					return false
				}
				if !polylineWithinLoops(pl, q.loops) {
					return false
				}
			}
			return true
		}
	case *geom.MultiPoint:
		if len(q.loops) > 0 {
			for i := 0; i < geometry.NumPoints(); i++ {
				s2pt := pointFromPoint(geometry.Point(i))
				inside := false
				for _, l := range q.loops {
					if l.ContainsPoint(s2pt) {
						inside = true
						break
					}
				}
				if !inside {
					return false
				}
			}
			return true
		}
	}
	return false
}

func multiPolygonContainsLoop(g *geom.MultiPolygon, l *s2.Loop) bool {
	for i := range g.NumPolygons() {
		p := g.Polygon(i)
		s2loop, err := loopFromPolygon(p)
		if err != nil {
			return false
		}
		if Contains(s2loop, l) {
			return true
		}
	}
	return false
}

// polylineWithinMultiPolygonLoops returns true if the polyline is fully contained
// by at least one component polygon of the MultiPolygon.
func polylineWithinMultiPolygonLoops(g *geom.MultiPolygon, pl *s2.Polyline) bool {
	for i := range g.NumPolygons() {
		p := g.Polygon(i)
		s2loop, err := loopFromPolygon(p)
		if err != nil {
			continue
		}
		if polylineWithinLoop(pl, s2loop) {
			return true
		}
	}
	return false
}

// multiPolygonContainsPoint returns true if any component polygon of the
// MultiPolygon contains the given point.
func multiPolygonContainsPoint(g *geom.MultiPolygon, pt s2.Point) bool {
	for i := range g.NumPolygons() {
		p := g.Polygon(i)
		s2loop, err := loopFromPolygon(p)
		if err != nil {
			continue
		}
		if s2loop.ContainsPoint(pt) {
			return true
		}
	}
	return false
}

// returns true if the geometry represented by g contains the given point/polygon.
// g is the geom.T representation of the value which is the stored in the DB.
func (q GeoQueryData) contains(g geom.T) bool {
	x.AssertTruef(q.pt != nil || len(q.loops) > 0 || len(q.polylines) > 0 || len(q.pts) > 0,
		"At least a point, loop, polyline, or point set should be defined.")
	switch v := g.(type) {
	case *geom.Polygon:
		if q.pt != nil {
			return polygonContainsCoord(v, q.pt)
		}

		s2loop, err := loopFromPolygon(v)
		if err != nil {
			return false
		}

		// Input could be a multipolygon, in which q.loops would have more than 1 loop. Each loop
		// in the query should be part of the s2loop.
		for _, l := range q.loops {
			if !Contains(s2loop, l) {
				return false
			}
		}
		if len(q.loops) > 0 {
			return true
		}

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

		return false
	case *geom.MultiPolygon:
		if q.pt != nil {
			for j := range v.NumPolygons() {
				if polygonContainsCoord(v.Polygon(j), q.pt) {
					return true
				}
			}

			return false
		}

		if len(q.loops) > 0 {
			// All the loops that are part of the query should be part of some loop of v.
			for _, l := range q.loops {
				if !multiPolygonContainsLoop(v, l) {
					return false
				}
			}
			return true
		}

		// MultiPolygon contains LineString/MultiLineString: each polyline must be
		// fully contained by at least one component polygon.
		if len(q.polylines) > 0 {
			for _, pl := range q.polylines {
				if !polylineWithinMultiPolygonLoops(v, pl) {
					return false
				}
			}
			return true
		}

		// MultiPolygon contains MultiPoint: each point must be inside at least
		// one component polygon.
		if len(q.pts) > 0 {
			for _, p := range q.pts {
				if !multiPolygonContainsPoint(v, p) {
					return false
				}
			}
			return true
		}

		return false
	case *geom.LineString:
		// A LineString can contain a point if the point lies on the line.
		if q.pt != nil {
			pl, err := polylineFromLineString(v)
			if err != nil {
				return false
			}
			return pointOnPolyline(*q.pt, pl)
		}
		// LineString containing a polygon, polyline, or multipoint is not supported
		// (a 1D object cannot contain a 2D region, and line-contains-line is a
		// complex subset check we deliberately skip).
		return false
	case *geom.MultiLineString:
		// A MultiLineString can contain a point if the point lies on any component line.
		if q.pt != nil {
			for i := 0; i < v.NumLineStrings(); i++ {
				pl, err := polylineFromLineString(v.LineString(i))
				if err != nil {
					return false
				}
				if pointOnPolyline(*q.pt, pl) {
					return true
				}
			}
			return false
		}
		return false
	case *geom.MultiPoint:
		// A MultiPoint can contain a point if any stored point matches the query point.
		if q.pt != nil {
			for i := 0; i < v.NumPoints(); i++ {
				s2pt := pointFromPoint(v.Point(i))
				if s2pt.ApproxEqual(*q.pt) {
					return true
				}
			}
			return false
		}
		return false
	default:
		// We will only consider polygons for contains queries.
		return false
	}
}

func polygonContainsCoord(v *geom.Polygon, pt *s2.Point) bool {
	for i := range v.NumLinearRings() {
		r := v.LinearRing(i)
		ll := s2.LatLngFromPoint(*pt)
		p := []float64{ll.Lng.Degrees(), ll.Lat.Degrees()}
		if xy.IsPointInRing(r.Layout(), p, r.FlatCoords()) {
			return true
		}
	}

	return false
}

// returns true if the geometry represented by uid/attr intersects the given loop or point
func (q GeoQueryData) intersects(g geom.T) bool {
	x.AssertTruef(len(q.loops) > 0 || len(q.polylines) > 0 || len(q.pts) > 0,
		"Loop, polyline, or points should be defined for intersects.")
	switch v := g.(type) {
	case *geom.Point:
		return q.pointIntersectsQuery(pointFromPoint(v))

	case *geom.Polygon:
		l, err := loopFromPolygon(v)
		if err != nil {
			return false
		}
		return q.loopIntersectsQuery(l)

	case *geom.MultiPolygon:
		for i := range v.NumPolygons() {
			l, err := loopFromPolygon(v.Polygon(i))
			if err != nil {
				return false
			}
			if q.loopIntersectsQuery(l) {
				return true
			}
		}
		return false

	case *geom.LineString:
		pl, err := polylineFromLineString(v)
		if err != nil {
			return false
		}
		return q.polylineIntersectsQuery(pl)

	case *geom.MultiLineString:
		for i := 0; i < v.NumLineStrings(); i++ {
			pl, err := polylineFromLineString(v.LineString(i))
			if err != nil {
				return false
			}
			if q.polylineIntersectsQuery(pl) {
				return true
			}
		}
		return false

	case *geom.MultiPoint:
		for i := 0; i < v.NumPoints(); i++ {
			if q.pointIntersectsQuery(pointFromPoint(v.Point(i))) {
				return true
			}
		}
		return false

	default:
		return false
	}
}

// pointIntersectsQuery returns true if a point intersects any query geometry.
func (q GeoQueryData) pointIntersectsQuery(p s2.Point) bool {
	for _, l := range q.loops {
		if l.ContainsPoint(p) {
			return true
		}
	}
	for _, pl := range q.polylines {
		if pointOnPolyline(p, pl) {
			return true
		}
	}
	for _, qpt := range q.pts {
		if p.ApproxEqual(qpt) {
			return true
		}
	}
	return false
}

// loopIntersectsQuery returns true if a loop intersects any query geometry.
func (q GeoQueryData) loopIntersectsQuery(l *s2.Loop) bool {
	for _, loop := range q.loops {
		if Intersects(l, loop) {
			return true
		}
	}
	for _, pl := range q.polylines {
		if polylineIntersectsLoop(pl, l) {
			return true
		}
	}
	for _, qpt := range q.pts {
		if l.ContainsPoint(qpt) {
			return true
		}
	}
	return false
}

// polylineIntersectsQuery returns true if a polyline intersects any query geometry.
func (q GeoQueryData) polylineIntersectsQuery(pl *s2.Polyline) bool {
	for _, loop := range q.loops {
		if polylineIntersectsLoop(pl, loop) {
			return true
		}
	}
	for _, qpl := range q.polylines {
		if polylinesIntersect(pl, qpl) {
			return true
		}
	}
	for _, qpt := range q.pts {
		if pointOnPolyline(qpt, pl) {
			return true
		}
	}
	return false
}

const lineContainsPointEpsilon = s1.Angle(1e-9)

// pointOnPolyline returns true if pt lies on any segment of pl within epsilon tolerance.
func pointOnPolyline(pt s2.Point, pl *s2.Polyline) bool {
	pts := []s2.Point(*pl)
	for i := 0; i < len(pts)-1; i++ {
		if s2.DistanceFromSegment(pt, pts[i], pts[i+1]) <= lineContainsPointEpsilon {
			return true
		}
	}
	return false
}

// polylineWithinLoops returns true if the entire polyline is within at least one of the loops.
func polylineWithinLoops(pl *s2.Polyline, loops []*s2.Loop) bool {
	for _, loop := range loops {
		if polylineWithinLoop(pl, loop) {
			return true
		}
	}
	return false
}

func polylineWithinLoop(pl *s2.Polyline, loop *s2.Loop) bool {
	pts := []s2.Point(*pl)
	// All vertices must be inside the loop.
	for _, pt := range pts {
		if !loop.ContainsPoint(pt) {
			return false
		}
	}
	// No segment may cross any loop edge.
	for i := 0; i < len(pts)-1; i++ {
		for j := 0; j < loop.NumEdges(); j++ {
			v0 := loop.Vertex(j)
			v1 := loop.Vertex(j + 1)
			if s2.CrossingSign(pts[i], pts[i+1], v0, v1) == s2.Cross {
				return false
			}
		}
	}
	return true
}

// polylineIntersectsLoop returns true if any part of the polyline intersects the loop.
func polylineIntersectsLoop(pl *s2.Polyline, loop *s2.Loop) bool {
	pts := []s2.Point(*pl)
	// Check if any vertex is inside the loop.
	for _, pt := range pts {
		if loop.ContainsPoint(pt) {
			return true
		}
	}
	// Check if any segment crosses any loop edge.
	for i := 0; i < len(pts)-1; i++ {
		for j := 0; j < loop.NumEdges(); j++ {
			v0 := loop.Vertex(j)
			v1 := loop.Vertex(j + 1)
			if s2.CrossingSign(pts[i], pts[i+1], v0, v1) == s2.Cross {
				return true
			}
		}
	}
	return false
}

func polylinesIntersect(a, b *s2.Polyline) bool {
	ptsA := []s2.Point(*a)
	ptsB := []s2.Point(*b)
	for i := 0; i < len(ptsA)-1; i++ {
		for j := 0; j < len(ptsB)-1; j++ {
			if s2.CrossingSign(ptsA[i], ptsA[i+1], ptsB[j], ptsB[j+1]) == s2.Cross {
				return true
			}
		}
	}
	// Also check if any endpoint of one is on a segment of the other.
	for _, pt := range ptsA {
		if pointOnPolyline(pt, b) {
			return true
		}
	}
	for _, pt := range ptsB {
		if pointOnPolyline(pt, a) {
			return true
		}
	}
	return false
}

// MatchGeo matches values and GeoQueryData and ensures that the value actually
// matches the query criteria.
func MatchGeo(value *pb.TaskValue, q *GeoQueryData) bool {
	valBytes := value.Val
	if bytes.Equal(valBytes, nil) {
		return false
	}
	vType := value.ValType
	if TypeID(vType) != GeoID {
		return false
	}
	src := ValueForType(BinaryID)
	src.Value = valBytes
	gc, err := Convert(src, GeoID)
	if err != nil {
		return false
	}
	g := gc.Value.(geom.T)
	return q.MatchesFilter(g)
}
