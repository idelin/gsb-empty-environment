# spacetime — 4D (3D + Time) BVH Trajectory Indexer

A Go (standard library only) spatiotemporal index for moving-object
trajectories. Every trajectory leg is a 4D line segment in (x, y, z, t);
the index stores its 4D AABB in a bounding-volume hierarchy and answers
window queries such as:

> "all trajectory segments that entered cube [x,y,z]..[X,Y,Z] above 100m
> altitude between 14:00 and 14:10"

## Features

- `BVH.Insert` / `BVH.InsertTrajectory` — incremental indexing of UAV
  cruise trajectories (polyline + timestamps).
- `BVH.Build` / `BVH.Rebuild` — bulk loading (top-down median split) for a
  balanced, low-overlap tree.
- `BVH.Query` — fast conservative answer via 4D AABB pruning.
- `BVH.QueryExact` — exact 4D segment-vs-box clipping (slab method);
  returns each fragment's entry/exit timestamps and 3D positions.
- `BVH.LastQueryStats` — traversal counters proving pruning effectiveness.

## Layout

- `bvh.go` — the index (types `Vec3`, `Segment`, `AABB4`, `BVH`, `Hit`).
- `bvh_test.go` — unit, correctness-vs-brute-force and performance tests.
- `cmd/demo/main.go` — runnable acceptance scenario.

## Run

```sh
go test -v ./...   # tests
go run ./cmd/demo  # demo: 3005 drones, 120k segments, 14:00-14:10 / z>=100m query
```

Sample demo output:

```
indexed 120006 trajectory segments (3005 drones)
query: zone x[2000,2400] y[3000,3400] z>=100m, time 14:00:00-14:10:00
found 51 matching trajectory fragments in 45µs
pruning: visited 71 nodes / 20 leaves, scanned 293 of 120006 segments (0.24%)
  alpha   leg#0  14:03:50 -> 14:04:00  enter (2000,3200,150m) exit (2200,3200,150m)
  epsilon leg#0  14:07:30 -> 14:08:30  enter (2000,3000,120m) exit (2400,3400,120m)
  ...
```
