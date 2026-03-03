/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package types

import (
	"testing"

	"github.com/stretchr/testify/require"
	geom "github.com/twpayne/go-geom"
	"github.com/twpayne/go-geom/encoding/geojson"
)

func TestConvertToGeoJson_Point(t *testing.T) {
	s := `[1.0, 2.0] `
	b, err := convertToGeom(s)
	require.NoError(t, err)
	require.Equal(t, geom.Coord{1, 2}, b.(*geom.Point).Coords())
}

func TestConvertToGeoJson_Poly(t *testing.T) {
	s := `[[[1.123, 2.543], [-3.23, 4.123], [4.43, -6.123], [1.123, 2.543]]]`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	require.Equal(t,
		[][]geom.Coord{{{1.123, 2.543}, {-3.23, 4.123}, {4.43, -6.123}, {1.123, 2.543}}},
		b.(*geom.Polygon).Coords())
	_, err = geojson.Marshal(b)
	require.NoError(t, err)
}

func TestConvertToGeoJson_PolyError1(t *testing.T) {
	s := `[[1.76, -2.234], [3.543, 4.534], [4.54, 6.213]]`
	_, err := convertToGeom(s)
	require.Error(t, err)
}

func TestConvertToGeoJson_PolyError2(t *testing.T) {
	s := `[[1.253, 2.2343], [-4.563, 6.1231], []]`
	_, err := convertToGeom(s)
	require.Error(t, err)
}

func TestConvertToGeoJson_LineString(t *testing.T) {
	s := `{"type":"LineString","coordinates":[[1.0,2.0],[3.0,4.0],[5.0,6.0]]}`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	ls, ok := b.(*geom.LineString)
	require.True(t, ok)
	require.Equal(t, 3, ls.NumCoords())
}

func TestConvertToGeoJson_LineStringTwoCoords(t *testing.T) {
	s := `{"type":"LineString","coordinates":[[1.0,2.0],[3.0,4.0]]}`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	ls, ok := b.(*geom.LineString)
	require.True(t, ok)
	require.Equal(t, 2, ls.NumCoords())
}

func TestConvertToGeoJson_LineStringSingleCoordError(t *testing.T) {
	s := `{"type":"LineString","coordinates":[[1.0,2.0]]}`
	_, err := convertToGeom(s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 2 coordinates")
}

func TestConvertToGeoJson_MultiLineString(t *testing.T) {
	s := `{"type":"MultiLineString","coordinates":[[[1.0,2.0],[3.0,4.0]],[[5.0,6.0],[7.0,8.0],[9.0,10.0]]]}`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	mls, ok := b.(*geom.MultiLineString)
	require.True(t, ok)
	require.Equal(t, 2, mls.NumLineStrings())
}

func TestConvertToGeoJson_MultiLineStringEmptyError(t *testing.T) {
	s := `{"type":"MultiLineString","coordinates":[]}`
	_, err := convertToGeom(s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 1 line")
}

func TestConvertToGeoJson_MultiLineStringSingleCoordLineError(t *testing.T) {
	s := `{"type":"MultiLineString","coordinates":[[[1.0,2.0]]]}`
	_, err := convertToGeom(s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 2 coordinates")
}

func TestConvertToGeoJson_MultiPoint(t *testing.T) {
	s := `{"type":"MultiPoint","coordinates":[[1.0,2.0],[3.0,4.0],[5.0,6.0]]}`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	mp, ok := b.(*geom.MultiPoint)
	require.True(t, ok)
	require.Equal(t, 3, mp.NumPoints())
}

func TestConvertToGeoJson_MultiPointSingle(t *testing.T) {
	s := `{"type":"MultiPoint","coordinates":[[1.0,2.0]]}`
	b, err := convertToGeom(s)
	require.NoError(t, err)
	mp, ok := b.(*geom.MultiPoint)
	require.True(t, ok)
	require.Equal(t, 1, mp.NumPoints())
}

func TestConvertToGeoJson_MultiPointEmptyError(t *testing.T) {
	s := `{"type":"MultiPoint","coordinates":[]}`
	_, err := convertToGeom(s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 1 point")
}
