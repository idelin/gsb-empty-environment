# Mip-safe texture atlas planner

`taskpkg` is a Go atlas planner for asset pipelines. It packs up to 12
different-sized textures into a power-of-two atlas while keeping at least one
unused pixel of bleed border around every texture at every mip level.

## Mip geometry

For a plan whose highest mip index is `maxMipLevel`:

- level-zero alignment and border are both `2^maxMipLevel`;
- a texture's valid slot is rounded up to that alignment (and to at least one
  coarse pixel in each dimension);
- at level `k`, valid and expanded regions are exact `2^k` subdivisions of
  level zero;
- the border is `2^(maxMipLevel-k)`, so it is at least one pixel at every level.

This is why small inputs and high mip counts do not use a fixed two-pixel
padding. For `maxMipLevel == 5`, for example, the level-zero border is 32
pixels and the coarsest border is one pixel.

## Packing policy

- atlas width and height are powers of two;
- rectangles may be rotated by 90 degrees;
- optimization order is area, then the longer atlas side, then the per-ID
  position vector;
- positions are compared lexicographically as `(X, Y)` for IDs in ascending
  order; an unrotated placement wins an identical position tie.

## API

```go
package main

import (
	"fmt"

	"example.org/domainreissue/q045"
)

func main() {
	atlas, err := taskpkg.Plan([]taskpkg.Rect{
		{ID: 7, Width: 13, Height: 5},
		{ID: 2, Width: 3, Height: 9},
	}, 4)
	if err != nil {
		panic(err)
	}

	p, _ := atlas.PlacementByID(7)
	fmt.Printf("atlas %dx%d, expanded=%+v, valid=%+v\\n",
		atlas.Width, atlas.Height, p.Expanded, p.Valid)

	m, _ := atlas.MipRegion(7, 4)
	fmt.Printf("level 4: valid=%+v expanded=%+v border=%d\\n",
		m.Valid, m.Expanded, m.Border)
}
```

`Plan` takes the highest mip index. `PlanMipLevels` takes the number of mip
levels. `Build`, `BuildAtlas`, `PlanAtlas`, and `Pack` are aliases.

Each `Placement` contains level-zero `Content`, `Valid`, and `Expanded`
regions plus a `Mips` slice. `Content` is the actual texture footprint,
`Valid` is the aligned slot that may include internal padding, and `Expanded`
is the slot plus the bleed border. `Atlas.Validate` checks nesting, mip
correspondence, and non-overlap at every level.
