/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package types

import (
	"encoding/binary"
	"math/rand"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/twpayne/go-geom"
	"github.com/twpayne/go-geom/encoding/wkb"
)

func queryTokens(qt QueryType, data string, maxDistance float64) ([]string, *GeoQueryData, error) {
	// Try to parse the data as geo type.
	geoData := strings.Replace(data, "'", "\"", -1)
	src := ValueForType(StringID)
	src.Value = []byte(geoData)
	gc, err := Convert(src, GeoID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Cannot decode given geoJson input")
	}
	g := gc.Value.(geom.T)

	return queryTokensGeo(qt, g, maxDistance)
}

func formData(t *testing.T, str string) string {
	p, err := loadPolygon(str)
	require.NoError(t, err)

	d, err := wkb.Marshal(p, binary.LittleEndian)
	require.NoError(t, err)

	src := ValueForType(GeoID)
	src.Value = d
	gd, err := Convert(src, StringID)
	require.NoError(t, err)
	gb := gd.Value.(string)
	return gb
}

func formDataPoint(t *testing.T, p *geom.Point) string {
	d, err := wkb.Marshal(p, binary.LittleEndian)
	require.NoError(t, err)

	src := ValueForType(GeoID)
	src.Value = d
	gd, err := Convert(src, StringID)
	require.NoError(t, err)
	gb := gd.Value.(string)

	return gb
}

func formDataPolygon(t *testing.T, g geom.T) string {
	d, err := wkb.Marshal(g, binary.LittleEndian)
	require.NoError(t, err)

	src := ValueForType(GeoID)
	src.Value = d
	gd, err := Convert(src, StringID)
	require.NoError(t, err)
	gb := gd.Value.(string)

	return gb
}

func TestQueryTokensPolygon(t *testing.T) {
	data := formData(t, "testdata/zip.json")

	qtypes := []QueryType{QueryTypeWithin, QueryTypeIntersects}
	for _, qt := range qtypes {
		toks, qd, err := queryTokens(qt, data, 0.0)
		require.NoError(t, err)

		if qt == QueryTypeWithin {
			require.Len(t, toks, 18)
		} else {
			require.Len(t, toks, 67)
		}
		require.NotNil(t, qd)
		require.Equal(t, qd.qtype, qt)
		require.NotZero(t, len(qd.loops))
		require.Nil(t, qd.pt)
	}
}

func TestQueryTokensPolygonError(t *testing.T) {
	data := formData(t, "testdata/zip.json")
	qtypes := []QueryType{QueryTypeNear}
	for _, qt := range qtypes {
		_, _, err := queryTokens(qt, data, 0.0)
		require.Error(t, err)
	}
}

func TestQueryTokensPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)

	qtypes := []QueryType{QueryTypeContains}
	for _, qt := range qtypes {
		toks, qd, err := queryTokens(qt, data, 0.0)
		require.NoError(t, err)

		switch qt {
		case QueryTypeWithin:
			require.Len(t, toks, 1)
		case QueryTypeContains:
			require.Len(t, toks, MaxCellLevel-MinCellLevel+1)
		default:
			require.Len(t, toks, MaxCellLevel-MinCellLevel+2)
		}
		require.NotNil(t, qd)
		require.Equal(t, qd.qtype, qt)
		require.Equal(t, 0, len(qd.loops))
		require.NotNil(t, qd.pt)
	}
}

func TestQueryTokensNear(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)

	toks, qd, err := queryTokens(QueryTypeNear, data, 1000.0)
	require.NoError(t, err)

	require.Equal(t, len(toks), 56)
	require.NotNil(t, qd)
	require.Equal(t, qd.qtype, QueryTypeIntersects)
	require.Equal(t, 1, len(qd.loops))
	require.Nil(t, qd.pt)
}

func TestQueryTokensNearError(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)

	_, _, err := queryTokens(QueryTypeNear, data, 0.0)
	require.Error(t, err) // no max distance
}

/*
func TestMatchesFilterWithinPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.NoError(t, err)

	// Poly contains point
	p2 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	require.True(t, qd.MatchesFilter(p2))

	// Poly doesn't contain point
	p3 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-123.082506, 37.4249518})
	require.False(t, qd.MatchesFilter(p3))

	// Poly within poly not supported
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122.1, 37.1}, {-122.9, 37.1}, {-122.9, 37.9}, {-122.1, 37.9}, {-122.1, 37.1}},
	})
	// Poly containment not supported
	require.False(t, qd.MatchesFilter(poly))
}
*/

func TestMatchesFilterContainsPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)

	// Points aren't returned for contains queries
	p2 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	require.False(t, qd.MatchesFilter(p2))

	// Polygon contains
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 37}, {-123, 37}, {-123, 38}, {-122, 38}, {-122, 37}},
	})
	require.True(t, qd.MatchesFilter(poly))

	// Polygon doesn't contains
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 36}, {-123, 36}, {-123, 37}, {-122, 37}, {-122, 36}},
	})
	require.False(t, qd.MatchesFilter(poly))

	// Multipolygon contains
	us, err := loadPolygon("testdata/us.json")
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(us))

	// Coordinates for Honolulu Airport.
	p = geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-157.9197, 21.33})
	data = formDataPoint(t, p)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(us))

	// Coordinates for Alaska
	p = geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-153.369141, 66.160507})
	data = formDataPoint(t, p)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(us))

	// Multipolygon doesn't contain
	p = geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{77.224249103, 28.6077159025})
	data = formDataPoint(t, p)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.False(t, qd.MatchesFilter(us))

	// Multipolygon contains another polygon
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-112, 39}, {-113, 39}, {-113, 40}, {-112, 40}, {-112, 39}},
	})
	data = formDataPolygon(t, poly)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(us))

	multipoly := geom.NewMultiPolygon(geom.XY).MustSetCoords([][][]geom.Coord{
		{{{-112, 39}, {-113, 39}, {-113, 40}, {-112, 40}, {-112, 39}}},
		{{{71.09, 42.35}, {72.09, 42.35}, {72.09, 41.35}, {71.09, 41.35}, {71.09, 42.35}}},
	})
	data = formDataPolygon(t, multipoly)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.False(t, qd.MatchesFilter(us))

	// Test with a different polygon (Sudan)
	sudan, err := loadPolygon("testdata/sudan.json")
	require.NoError(t, err)

	p = geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{0, 0})
	data = formDataPoint(t, p)
	_, qd, err = queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.False(t, qd.MatchesFilter(sudan))
}

/*
func TestMatchesFilterIntersectsPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)

	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// Same point
	p2 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	require.True(t, qd.MatchesFilter(p2))

	// Different point
	p3 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-123.082506, 37.4249518})
	require.False(t, qd.MatchesFilter(p3))

	// containing poly
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 37}, {-123, 37}, {-123, 38}, {-122, 38}, {-122, 37}},
	})
	require.True(t, qd.MatchesFilter(poly))

	// Polygon doesn't contains
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 36}, {-123, 36}, {-123, 37}, {-122, 37}, {-122, 36}},
	})
	require.False(t, qd.MatchesFilter(poly))
}
*/

func TestMatchesFilterIntersectsPolygon(t *testing.T) {
	p := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 37}, {-123, 37}, {-123, 38}, {-122, 38}, {-122, 37}},
	})
	data := formDataPolygon(t, p)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// Poly contains point
	p2 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	require.True(t, qd.MatchesFilter(p2))

	// Poly doesn't contain point
	p3 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-123.082506, 37.4249518})
	require.False(t, qd.MatchesFilter(p3))

	// Poly contains poly
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122.1, 37.1}, {-122.9, 37.1}, {-122.9, 37.9}, {-122.1, 37.9}, {-122.1, 37.1}},
	})
	require.True(t, qd.MatchesFilter(poly))

	// Poly contained in poly
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-121, 36}, {-124, 36}, {-124, 39}, {-121, 39}, {-121, 36}},
	})
	require.True(t, qd.MatchesFilter(poly))

	// Poly intersecting poly
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-121.5, 36.5}, {-122.5, 36.5}, {-122.5, 37.5}, {-121.5, 37.5}, {-121.5, 36.5}},
	})
	require.True(t, qd.MatchesFilter(poly))

	// Poly not intersecting poly
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-120, 35}, {-121, 35}, {-121, 36}, {-120, 36}, {-120, 35}},
	})
	require.False(t, qd.MatchesFilter(poly))

	// These two polygons don't intersect.
	polyOut := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{
			{-122.4989104270935, 37.736953437345356},
			{-122.50504732131958, 37.729096212099975},
			{-122.49515533447264, 37.732049133202324},
			{-122.4989104270935, 37.736953437345356},
		},
	})

	poly2 := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{
			{-122.5033039, 37.7334601},
			{-122.503128, 37.7335189},
			{-122.5031222, 37.7335205},
			{-122.5030813, 37.7335868},
			{-122.5031511, 37.73359},
			{-122.5031933, 37.7335916},
			{-122.5032228, 37.7336022},
			{-122.5032697, 37.7335937},
			{-122.5033194, 37.7335874},
			{-122.5033723, 37.7335518},
			{-122.503369, 37.7335068},
			{-122.5033462, 37.7334474},
			{-122.5033039, 37.7334601},
		},
	})
	data = formDataPolygon(t, polyOut)
	_, qd, err = queryTokens(QueryTypeIntersects, data, 0.0)
	require.False(t, qd.MatchesFilter(poly2))
	require.NoError(t, err)

	// Multipolygon intersects
	us, err := loadPolygon("testdata/us.json")
	require.NoError(t, err)

	// Multipoly intersecting poly
	poly = geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-121.5, 36.5}, {-122.5, 36.5}, {-122.5, 37.5}, {-121.5, 37.5}, {-121.5, 36.5}},
	})
	// Query input is the above polygon.
	data = formDataPolygon(t, poly)
	_, qd, err = queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(us))

	data = formDataPolygon(t, us)
	// Query input is US Multipolygon, it should return p2 as p2 lies within it.
	_, qd, err = queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(p2))

	// Multipolygon intersecting itself.
	require.True(t, qd.MatchesFilter(us))
}

func TestMatchesFilterWithinPolygon(t *testing.T) {
	us, err := loadPolygon("testdata/us.json")
	require.NoError(t, err)

	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-119.53, 37.86}, {-120.53, 37.86}, {-120.53, 36.86}, {-119.53, 36.86}, {-119.53, 37.86}},
	})
	data := formDataPolygon(t, us)
	_, qd, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.NoError(t, err)
	require.True(t, qd.MatchesFilter(poly))

	multipoly := geom.NewMultiPolygon(geom.XY).MustSetCoords([][][]geom.Coord{{
		{{-119.53, 37.86}, {-120.53, 37.86}, {-120.53, 36.86}, {-119.53, 36.86}, {-119.53, 37.86}},
		{{-115.13, 36.18}, {-116.13, 36.18}, {-116.13, 35.18}, {-115.13, 35.18}, {-115.13, 36.18}},
		{{-71.09, 42.35}, {-72.09, 42.35}, {-72.09, 41.35}, {-71.09, 41.35}, {-71.09, 42.35}},
	}})
	require.True(t, qd.MatchesFilter(multipoly))
}

func TestMatchesFilterNearPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeNear, data, 1000.0)
	require.NoError(t, err)

	// Same point
	p2 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	require.True(t, qd.MatchesFilter(p2))

	// Close point
	p3 := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.080668, 37.426753})
	require.True(t, qd.MatchesFilter(p3))

	// Far point
	p3 = geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-123.082506, 37.4249518})
	require.False(t, qd.MatchesFilter(p3))

	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122, 37}, {-123, 37}, {-123, 38}, {-122, 38}, {-122, 37}},
	})
	require.True(t, qd.MatchesFilter(poly))
}

// --- LineString/MultiLineString/MultiPoint filter tests ---
// Note: formDataPolygon accepts geom.T, so it works for all geo types.

func TestMatchesFilterWithinLineString(t *testing.T) {
	// Query: within a polygon containing the SF Bay Area
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.NoError(t, err)

	// LineString fully inside the polygon
	lsInside := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-122.0, 37.8},
	})
	require.True(t, qd.MatchesFilter(lsInside))

	// LineString partially outside
	lsPartial := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-120.0, 37.5},
	})
	require.False(t, qd.MatchesFilter(lsPartial))

	// LineString completely outside
	lsOutside := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-119.0, 36.0}, {-118.0, 35.0},
	})
	require.False(t, qd.MatchesFilter(lsOutside))
}

func TestMatchesFilterWithinMultiLineString(t *testing.T) {
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.NoError(t, err)

	// Both lines inside
	mlsInside := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122.5, 37.3}, {-122.0, 37.5}},
		{{-121.5, 37.6}, {-121.8, 37.9}},
	})
	require.True(t, qd.MatchesFilter(mlsInside))

	// One line outside
	mlsPartial := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122.5, 37.3}, {-122.0, 37.5}},
		{{-119.0, 36.0}, {-118.0, 35.0}},
	})
	require.False(t, qd.MatchesFilter(mlsPartial))
}

func TestMatchesFilterWithinMultiPoint(t *testing.T) {
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.NoError(t, err)

	// All points inside
	mpInside := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-121.5, 37.8},
	})
	require.True(t, qd.MatchesFilter(mpInside))

	// One point outside
	mpPartial := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-119.0, 36.0},
	})
	require.False(t, qd.MatchesFilter(mpPartial))
}

func TestMatchesFilterContainsLineString(t *testing.T) {
	// Query with a point, test that stored LineString contains the point.
	// Use N-S oriented line (same longitude) so the point lies exactly on the great circle arc.
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.0, 37.5})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)

	// LineString along same longitude that passes through the point (N-S line)
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.0, 38.0},
	})
	require.True(t, qd.MatchesFilter(ls))

	// LineString that doesn't pass through the point
	lsFar := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-123.0, 38.0}, {-123.0, 39.0},
	})
	require.False(t, qd.MatchesFilter(lsFar))
}

func TestMatchesFilterContainsMultiLineString(t *testing.T) {
	// Use N-S oriented lines so points lie exactly on the great circle arc.
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.0, 37.5})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)

	// MultiLineString where one line passes through the point (N-S line at same longitude)
	mls := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123.0, 38.0}, {-123.0, 39.0}}, // doesn't pass through
		{{-122.0, 37.0}, {-122.0, 38.0}}, // passes through (N-S at same lng)
	})
	require.True(t, qd.MatchesFilter(mls))

	// MultiLineString where no line passes through
	mlsFar := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123.0, 38.0}, {-123.0, 39.0}},
		{{-124.0, 39.0}, {-124.0, 40.0}},
	})
	require.False(t, qd.MatchesFilter(mlsFar))
}

func TestMatchesFilterContainsMultiPoint(t *testing.T) {
	p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{-122.082506, 37.4249518})
	data := formDataPoint(t, p)
	_, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)

	// MultiPoint containing the same point
	mp := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-123.0, 38.0},
		{-122.082506, 37.4249518},
	})
	require.True(t, qd.MatchesFilter(mp))

	// MultiPoint not containing the query point
	mpFar := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-123.0, 38.0},
		{-124.0, 39.0},
	})
	require.False(t, qd.MatchesFilter(mpFar))
}

func TestMatchesFilterIntersectsLineString(t *testing.T) {
	// Intersect query with a polygon
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// LineString fully inside polygon
	lsInside := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-122.0, 37.8},
	})
	require.True(t, qd.MatchesFilter(lsInside))

	// LineString crossing polygon boundary
	lsCrossing := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5}, {-120.0, 37.5},
	})
	require.True(t, qd.MatchesFilter(lsCrossing))

	// LineString completely outside
	lsOutside := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-119.0, 36.0}, {-118.0, 35.0},
	})
	require.False(t, qd.MatchesFilter(lsOutside))
}

func TestMatchesFilterIntersectsMultiLineString(t *testing.T) {
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// MultiLineString with one line inside
	mlsPartial := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-122.5, 37.5}, {-122.0, 37.8}},
		{{-119.0, 36.0}, {-118.0, 35.0}},
	})
	require.True(t, qd.MatchesFilter(mlsPartial))

	// MultiLineString completely outside
	mlsOutside := geom.NewMultiLineString(geom.XY).MustSetCoords([][]geom.Coord{
		{{-119.0, 36.0}, {-118.0, 35.0}},
		{{-117.0, 34.0}, {-116.0, 33.0}},
	})
	require.False(t, qd.MatchesFilter(mlsOutside))
}

func TestMatchesFilterIntersectsMultiPoint(t *testing.T) {
	poly := geom.NewPolygon(geom.XY).MustSetCoords([][]geom.Coord{
		{{-123, 37}, {-121, 37}, {-121, 38}, {-123, 38}, {-123, 37}},
	})
	data := formDataPolygon(t, poly)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// MultiPoint with one point inside
	mpPartial := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.5},
		{-119.0, 36.0},
	})
	require.True(t, qd.MatchesFilter(mpPartial))

	// MultiPoint completely outside
	mpOutside := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-119.0, 36.0},
		{-117.0, 34.0},
	})
	require.False(t, qd.MatchesFilter(mpOutside))
}

func TestQueryTokensLineStringContains(t *testing.T) {
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.5, 37.5},
	})
	data := formDataPolygon(t, ls)
	toks, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.NotEmpty(t, toks)
	require.NotNil(t, qd)
	require.NotEmpty(t, qd.polylines)
}

func TestQueryTokensLineStringIntersects(t *testing.T) {
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.5, 37.5},
	})
	data := formDataPolygon(t, ls)
	toks, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)
	require.NotEmpty(t, toks)
	require.NotNil(t, qd)
	require.NotEmpty(t, qd.polylines)
}

func TestQueryTokensLineStringWithinError(t *testing.T) {
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.5, 37.5},
	})
	data := formDataPolygon(t, ls)
	_, _, err := queryTokens(QueryTypeWithin, data, 0.0)
	require.Error(t, err)
}

func TestQueryTokensLineStringNearError(t *testing.T) {
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.5, 37.5},
	})
	data := formDataPolygon(t, ls)
	_, _, err := queryTokens(QueryTypeNear, data, 1000.0)
	require.Error(t, err)
}

func TestQueryTokensMultiPointContains(t *testing.T) {
	mp := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.5, 37.5},
	})
	data := formDataPolygon(t, mp)
	toks, qd, err := queryTokens(QueryTypeContains, data, 0.0)
	require.NoError(t, err)
	require.NotEmpty(t, toks)
	require.NotNil(t, qd)
	require.NotEmpty(t, qd.pts)
}

func TestIntersectsLineStringWithLineStringQuery(t *testing.T) {
	// Intersect query argument is a LineString
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.5, 37.0}, {-122.5, 38.0},
	})
	data := formDataPolygon(t, ls)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// Stored LineString that crosses the query line
	lsCross := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-123.0, 37.5}, {-122.0, 37.5},
	})
	require.True(t, qd.MatchesFilter(lsCross))

	// Stored LineString that doesn't cross
	lsNoIntersect := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-121.0, 36.0}, {-120.0, 35.0},
	})
	require.False(t, qd.MatchesFilter(lsNoIntersect))
}

func TestIntersectsMultiPointWithLineStringQuery(t *testing.T) {
	// Use N-S line so points lie exactly on the great circle arc.
	ls := geom.NewLineString(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.0}, {-122.0, 38.0},
	})
	data := formDataPolygon(t, ls)
	_, qd, err := queryTokens(QueryTypeIntersects, data, 0.0)
	require.NoError(t, err)

	// MultiPoint with a point on the line (same longitude)
	mp := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-122.0, 37.5},
	})
	require.True(t, qd.MatchesFilter(mp))

	// MultiPoint not on the line
	mpFar := geom.NewMultiPoint(geom.XY).MustSetCoords([]geom.Coord{
		{-123.0, 37.5},
	})
	require.False(t, qd.MatchesFilter(mpFar))
}

func BenchmarkMatchesFilterContainsPoint(b *testing.B) {
	us, _ := loadPolygon("testdata/us.json")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		p := geom.NewPoint(geom.XY).MustSetCoords(geom.Coord{
			(rand.Float64() * 360) - 180,
			(rand.Float64() * 180) - 90})

		d, _ := wkb.Marshal(p, binary.LittleEndian)
		src := ValueForType(GeoID)
		src.Value = d
		gd, _ := Convert(src, StringID)
		gb := gd.Value.(string)
		_, qd, _ := queryTokens(QueryTypeContains, gb, 0.0)
		qd.contains(us)
	}
}
