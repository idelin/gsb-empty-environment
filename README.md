# voxelsurf

Voxel-set surface area library. Input is a list of integer lattice
coordinates (duplicates are counted once); coordinates may span a huge range
while the voxel count stays small (<= 10000). The bounding box is never
allocated.

## API

- `Analyze(voxels []Point) Analysis` — coordinate-compression flood fill.
  Handles arbitrary coordinate spans.
- `AnalyzeDense(voxels []Point) Analysis` — naive per-cell reference model
  that allocates the padded bounding box. Only for small spans; used to
  cross-check `Analyze`.

## Results

`Analysis` reports:

- `TotalArea` — all exposed unit faces, including faces sealing a cavity.
- `ExternalArea` — exposed faces touching outside air only; sealed cavities
  are excluded.
- `CavityArea` / `CavityVolume` — cavity-sealing face count / enclosed cell
  count.
- `ExternalFaces` — sorted `Face` numbers as evidence for `ExternalArea`.
- `Cavities` — per-cavity `Volume` plus the sorted `Face` numbers that seal
  it.

A `Face` is a deterministic coordinate-based face number: the axis it is
perpendicular to, the plane position along that axis, and the two remaining
coordinates, e.g. `face|X|plane=2|u=1|v=1`.

## Method

Each axis is compressed to its sorted unique voxel coordinates. Any free
cell with a coordinate outside those sets lies in a completely empty plane;
all such gap slabs interconnect into a single outside-air skeleton, so only
"all-coordinate" free cells are ever flood-filled, and each of them is
exactly one original lattice cell. A flood is classified as a cavity only if
it never touches the skeleton; by the Loomis-Whitney isoperimetric bound any
sealed cavity satisfies `|C| <= n^(3/2)`, so floods exceeding that limit are
known to be outside air and are abandoned.

`TestCrossCheckRandom` / `TestCrossCheckStructured` verify `Analyze` against
`AnalyzeDense` on hundreds of random and structured small cases;
`TestLargeSpanSparse` verifies 10000 voxels scattered over a +/-1e9 range.

## Run

```sh
go test ./...
```
