package taskpkg

import (
	"errors"
	"fmt"
	"sort"
)

const (
	// MaxRects is the hard limit on the number of input textures.
	MaxRects = 12

	// MaxSupportedMipLevel keeps coarse-level scales representable by the
	// signed integer types used by the returned regions.
	MaxSupportedMipLevel = 30
)

var (
	// ErrNoRects is returned for an empty input set.
	ErrNoRects = errors.New("atlas: at least one rectangle is required")
	// ErrTooManyRects is returned when more than MaxRects rectangles are supplied.
	ErrTooManyRects = errors.New("atlas: more than 12 rectangles")
)

// Rect describes one input texture at mip level zero.
//
// ID identifies the texture in the asset pipeline and must be unique within a
// plan. Width and Height are the unpadded level-zero dimensions.
type Rect struct {
	ID     int
	Width  int
	Height int
}

// Item is an alias for Rect.
type Item = Rect

// Rectangle is an alias for Rect.
type Rectangle = Rect

// Region is an axis-aligned rectangle in atlas pixel coordinates.
type Region struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Empty reports whether the region has no area.
func (r Region) Empty() bool {
	return r.Width <= 0 || r.Height <= 0
}

// Right returns the exclusive right edge.
func (r Region) Right() int {
	return r.X + r.Width
}

// Bottom returns the exclusive bottom edge.
func (r Region) Bottom() int {
	return r.Y + r.Height
}

// Overlaps reports whether two regions share any pixel.
func (r Region) Overlaps(other Region) bool {
	return r.X < other.Right() && other.X < r.Right() &&
		r.Y < other.Bottom() && other.Y < r.Bottom()
}

// Contains reports whether other is completely inside r.
func (r Region) Contains(other Region) bool {
	return other.X >= r.X && other.Y >= r.Y &&
		other.Right() <= r.Right() && other.Bottom() <= r.Bottom()
}

// MipRegion describes one texture at one mip level.
//
// Content is the actual mip footprint. Valid is the aligned slot in which
// Content lives; Valid may contain transparent or edge-extended pixels. The
// texture's mipmap data must not be sampled outside Valid. Expanded adds the
// bleed border around Valid and must not overlap another placement's Expanded
// region at the same level.
type MipRegion struct {
	Level    int
	Content  Region
	Valid    Region
	Expanded Region
	// Border is the number of border pixels on each side at this level.
	Border int
}

// Placement is a level-zero placement and its corresponding regions at every
// mip level.
type Placement struct {
	ID      int
	Rotated bool

	// X and Y are the top-left corner of Expanded at level zero.
	X int
	Y int
	// Width and Height are the size of Expanded at level zero.
	Width  int
	Height int

	Content  Region
	Valid    Region
	Expanded Region
	Mips     []MipRegion
}

// RegionAt returns this placement's regions at level.
func (p Placement) RegionAt(level int) (MipRegion, bool) {
	if level < 0 || level >= len(p.Mips) {
		return MipRegion{}, false
	}
	return p.Mips[level], true
}

// MipLevel describes the atlas dimensions at a mip level.
type MipLevel struct {
	Level  int
	Width  int
	Height int
}

// Atlas is a complete, deterministic atlas plan.
type Atlas struct {
	// Width and Height are level-zero dimensions and are powers of two.
	Width  int
	Height int

	// MaxMipLevel is the highest mip level index present. MipLevels is
	// MaxMipLevel+1.
	MaxMipLevel int
	MipLevels   int

	// Alignment is the level-zero alignment/slot quantum, 2^MaxMipLevel.
	Alignment int
	// Border is the level-zero border on each side, equal to Alignment.
	Border int

	// Placements are in the same order as the input rectangles.
	Placements []Placement
	Levels     []MipLevel
}

// PlacementByID returns the placement for id.
func (a Atlas) PlacementByID(id int) (Placement, bool) {
	for i := range a.Placements {
		if a.Placements[i].ID == id {
			return a.Placements[i], true
		}
	}
	return Placement{}, false
}

// Level returns the atlas dimensions for level. The boolean is false when
// level is outside the planned range.
func (a Atlas) Level(level int) (MipLevel, bool) {
	if level < 0 || level >= len(a.Levels) {
		return MipLevel{}, false
	}
	return a.Levels[level], true
}

// MipRegion returns the regions for id at level.
func (a Atlas) MipRegion(id, level int) (MipRegion, bool) {
	p, ok := a.PlacementByID(id)
	if !ok {
		return MipRegion{}, false
	}
	return p.RegionAt(level)
}

// Plan computes a mip-safe atlas for rects.
//
// maxMipLevel is the highest mip index to plan for. For example, 0 means a
// single level, 1 means levels 0 and 1, and so on. The base-level alignment
// and border are both 2^maxMipLevel; consequently every placement has a
// one-pixel border at the coarsest level.
//
// The returned atlas minimizes area first and the longer side second. Equal
// solutions are resolved by comparing the position vectors of the IDs in
// ascending ID order. Positions are compared lexicographically as (X, Y), and
// an unrotated placement wins if positions are identical.
func Plan(rects []Rect, maxMipLevel int) (Atlas, error) {
	if len(rects) == 0 {
		return Atlas{}, ErrNoRects
	}
	if len(rects) > MaxRects {
		return Atlas{}, ErrTooManyRects
	}
	if maxMipLevel < 0 || maxMipLevel > MaxSupportedMipLevel {
		return Atlas{}, fmt.Errorf("atlas: max mip level %d outside [0,%d]", maxMipLevel, MaxSupportedMipLevel)
	}

	seen := make(map[int]struct{}, len(rects))
	for _, r := range rects {
		if r.Width <= 0 || r.Height <= 0 {
			return Atlas{}, fmt.Errorf("atlas: rectangle %d has non-positive size %dx%d", r.ID, r.Width, r.Height)
		}
		if _, ok := seen[r.ID]; ok {
			return Atlas{}, fmt.Errorf("atlas: duplicate rectangle ID %d", r.ID)
		}
		seen[r.ID] = struct{}{}
	}

	alignment := 1 << uint(maxMipLevel)
	packItems := make([]packItem, len(rects))
	var sumW, sumH int64
	for i, r := range rects {
		validW := ceilDiv(r.Width, alignment)
		validH := ceilDiv(r.Height, alignment)
		// Rounding to the alignment quantum guarantees that a valid slot
		// still exists at the coarsest level, even for 1-pixel inputs.
		validW = maxInt(validW, 1)
		validH = maxInt(validH, 1)
		item := packItem{
			index:  i,
			id:     r.ID,
			width:  int64(r.Width),
			height: int64(r.Height),
			validW: int64(validW),
			validH: int64(validH),
			expW:   int64(validW + 2),
			expH:   int64(validH + 2),
		}
		packItems[i] = item
		if item.expW > (int64(1)<<62)-sumW || item.expH > (int64(1)<<62)-sumH {
			return Atlas{}, fmt.Errorf("atlas: rectangle %d is too large to plan", r.ID)
		}
		sumW += item.expW
		sumH += item.expH
	}

	candidates := atlasCandidates(packItems, sumW, sumH)
	if len(candidates) == 0 {
		return Atlas{}, errors.New("atlas: no feasible power-of-two atlas dimensions")
	}

	for start := 0; start < len(candidates); {
		area := candidates[start].area
		longSide := candidates[start].longSide
		end := start
		for end < len(candidates) && candidates[end].area == area && candidates[end].longSide == longSide {
			end++
		}

		var best *Atlas
		var bestKey layoutKey
		for _, c := range candidates[start:end] {
			if !canFitOneByOne(packItems, c.width, c.height) {
				continue
			}
			any, ok := packAny(packItems, c.width, c.height)
			if !ok {
				continue
			}
			bestPack, ok := packBest(packItems, c.width, c.height, any)
			if !ok {
				bestPack = any
			}
			atlas := buildAtlas(rects, packItems, bestPack, c.width, c.height, alignment, maxMipLevel)
			key := atlasLayoutKey(atlas)
			if best == nil || key.less(bestKey) || (key.equal(bestKey) && atlas.Width < best.Width) {
				best = &atlas
				bestKey = key
			}
		}
		if best != nil {
			if err := best.Validate(); err != nil {
				return Atlas{}, fmt.Errorf("atlas: internal validation failed: %w", err)
			}
			return *best, nil
		}
		start = end
	}

	return Atlas{}, errors.New("atlas: no feasible power-of-two atlas dimensions")
}

// BuildAtlas is an alias for Plan.
func BuildAtlas(rects []Rect, maxMipLevel int) (Atlas, error) {
	return Plan(rects, maxMipLevel)
}

// PlanAtlas is an alias for Plan.
func PlanAtlas(rects []Rect, maxMipLevel int) (Atlas, error) {
	return Plan(rects, maxMipLevel)
}

// Pack is an alias for Plan.
func Pack(rects []Rect, maxMipLevel int) (Atlas, error) {
	return Plan(rects, maxMipLevel)
}

// Build is an alias for Plan.
func Build(rects []Rect, maxMipLevel int) (Atlas, error) {
	return Plan(rects, maxMipLevel)
}

// PlanMipLevels is like Plan but takes the number of mip levels instead of the
// highest level index.
func PlanMipLevels(rects []Rect, mipLevels int) (Atlas, error) {
	if mipLevels < 1 {
		return Atlas{}, fmt.Errorf("atlas: mip level count %d must be positive", mipLevels)
	}
	return Plan(rects, mipLevels-1)
}

func ceilDiv(v, d int) int {
	q := v / d
	if v%d != 0 {
		q++
	}
	return q
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// buildAtlas converts normalized-grid packing coordinates into pixel regions.
func buildAtlas(rects []Rect, items []packItem, pack []packPos, width, height int64, alignment, maxMipLevel int) Atlas {
	atlasW := int(width) * alignment
	atlasH := int(height) * alignment
	a := Atlas{
		Width:       atlasW,
		Height:      atlasH,
		MaxMipLevel: maxMipLevel,
		MipLevels:   maxMipLevel + 1,
		Alignment:   alignment,
		Border:      alignment,
		Placements:  make([]Placement, len(rects)),
		Levels:      make([]MipLevel, maxMipLevel+1),
	}
	for k := 0; k <= maxMipLevel; k++ {
		a.Levels[k] = MipLevel{Level: k, Width: atlasW >> uint(k), Height: atlasH >> uint(k)}
	}

	for i := range rects {
		item := items[i]
		pos := pack[i]
		contentW, contentH := int(item.width), int(item.height)
		validW, validH := int(item.validW), int(item.validH)
		if pos.rotated {
			contentW, contentH = contentH, contentW
			validW, validH = validH, validW
		}

		base := Region{
			X:      int(pos.x) * alignment,
			Y:      int(pos.y) * alignment,
			Width:  (validW + 2) * alignment,
			Height: (validH + 2) * alignment,
		}
		valid := Region{
			X:      base.X + alignment,
			Y:      base.Y + alignment,
			Width:  validW * alignment,
			Height: validH * alignment,
		}
		content := Region{
			X:      valid.X,
			Y:      valid.Y,
			Width:  contentW,
			Height: contentH,
		}

		pl := Placement{
			ID:       rects[i].ID,
			Rotated:  pos.rotated,
			X:        base.X,
			Y:        base.Y,
			Width:    base.Width,
			Height:   base.Height,
			Content:  content,
			Valid:    valid,
			Expanded: base,
			Mips:     make([]MipRegion, maxMipLevel+1),
		}
		for k := 0; k <= maxMipLevel; k++ {
			down := 1 << uint(k)
			border := alignment / down
			pl.Mips[k] = MipRegion{
				Level: k,
				Content: Region{
					X:      content.X / down,
					Y:      content.Y / down,
					Width:  ceilDiv(contentW, down),
					Height: ceilDiv(contentH, down),
				},
				Valid: Region{
					X:      valid.X / down,
					Y:      valid.Y / down,
					Width:  valid.Width / down,
					Height: valid.Height / down,
				},
				Expanded: Region{
					X:      base.X / down,
					Y:      base.Y / down,
					Width:  base.Width / down,
					Height: base.Height / down,
				},
				Border: border,
			}
		}
		a.Placements[i] = pl
	}
	return a
}

// layoutKey is the per-ID position vector used to resolve equal atlas
// candidates. Each entry corresponds to the ID in ascending order.
type layoutKey []layoutKeyEntry

type layoutKeyEntry struct {
	id      int
	y       int
	x       int
	rotated bool
}

func atlasLayoutKey(a Atlas) layoutKey {
	key := make(layoutKey, len(a.Placements))
	for i, p := range a.Placements {
		key[i] = layoutKeyEntry{id: p.ID, y: p.Y, x: p.X, rotated: p.Rotated}
	}
	sort.Slice(key, func(i, j int) bool { return key[i].id < key[j].id })
	return key
}

func (k layoutKey) less(other layoutKey) bool {
	n := len(k)
	if len(other) < n {
		n = len(other)
	}
	for i := 0; i < n; i++ {
		a, b := k[i], other[i]
		if a.x != b.x {
			return a.x < b.x
		}
		if a.y != b.y {
			return a.y < b.y
		}
		if a.rotated != b.rotated {
			return !a.rotated
		}
	}
	return len(k) < len(other)
}

func (k layoutKey) equal(other layoutKey) bool {
	if len(k) != len(other) {
		return false
	}
	for i := range k {
		if k[i] != other[i] {
			return false
		}
	}
	return true
}
