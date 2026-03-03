# Next Steps for S2 Geo Types

See `plans/s2-geo-types.md` for full status.

## What to do next session

1. **Commit** the `outputnode_graphql.go` fix (file is modified, not yet committed)
2. **Run query integration tests**: `go test ./query/... -run Geo -v -count=1`
3. **Debug any failures** from step 2
4. **End-to-end manual validation** — insert LineString/MultiLineString/MultiPoint via DQL and GraphQL, query with all 4 geo functions, verify response shapes
5. **Final commit + plan update**

## Cluster is already running
- Alpha, Zero, Ratel all up on `dgraph-org_default` network
- Image: `dgraph/dgraph:local` (rebuilt this session with all fixes)
- Alpha is healthy — assertion crash resolved

## Key finding this session
`completeGeoObject` in `query/outputnode_graphql.go` was missing switch cases for LineString/MultiLineString/MultiPoint. Fixed by adding `completeLineString`, `completeMultiLineString`, `completeMultiPoint`, and `writeCoordinateArray`. Not yet committed.
