package taskpkg

import (
	"fmt"
)

// Validate checks the structural invariants of an atlas plan:
//
//   - atlas dimensions are powers of two;
//   - every level has at least a one-pixel border around its valid slot;
//   - valid, content, and expanded regions are nested;
//   - regions at coarser levels are exact power-of-two subdivisions of the
//     level-zero regions;
//   - expanded regions never overlap, including at every mip level.
func (a Atlas) Validate() error {
	if a.Width <= 0 || a.Height <= 0 {
		return fmt.Errorf("atlas: invalid size %dx%d", a.Width, a.Height)
	}
	if !isPowerOfTwo(a.Width) || !isPowerOfTwo(a.Height) {
		return fmt.Errorf("atlas: dimensions %dx%d are not powers of two", a.Width, a.Height)
	}
	if a.MaxMipLevel < 0 || a.MipLevels != a.MaxMipLevel+1 {
		return fmt.Errorf("atlas: inconsistent mip count %d for max level %d", a.MipLevels, a.MaxMipLevel)
	}
	if a.Alignment != 1<<uint(a.MaxMipLevel) {
		return fmt.Errorf("atlas: alignment %d does not match max mip level %d", a.Alignment, a.MaxMipLevel)
	}
	if a.Border != a.Alignment {
		return fmt.Errorf("atlas: level-zero border %d does not match alignment %d", a.Border, a.Alignment)
	}
	if len(a.Levels) != a.MipLevels {
		return fmt.Errorf("atlas: %d level records for %d mip levels", len(a.Levels), a.MipLevels)
	}
	for k := 0; k <= a.MaxMipLevel; k++ {
		w, h := a.Width>>uint(k), a.Height>>uint(k)
		if w < 1 || h < 1 {
			return fmt.Errorf("atlas: level %d has empty dimensions", k)
		}
		lev := a.Levels[k]
		if lev.Level != k || lev.Width != w || lev.Height != h {
			return fmt.Errorf("atlas: level %d record is %+v, want %dx%d", k, lev, w, h)
		}
	}

	ids := make(map[int]struct{}, len(a.Placements))
	for _, p := range a.Placements {
		if _, ok := ids[p.ID]; ok {
			return fmt.Errorf("atlas: duplicate placement ID %d", p.ID)
		}
		ids[p.ID] = struct{}{}
		if len(p.Mips) != a.MipLevels {
			return fmt.Errorf("atlas: placement %d has %d mip regions, want %d", p.ID, len(p.Mips), a.MipLevels)
		}
		if p.Content.Empty() || p.Valid.Empty() || p.Expanded.Empty() {
			return fmt.Errorf("atlas: placement %d has an empty region", p.ID)
		}
		if !p.Expanded.Contains(p.Valid) {
			return fmt.Errorf("atlas: placement %d valid region %+v is outside expanded region %+v", p.ID, p.Valid, p.Expanded)
		}
		if !p.Valid.Contains(p.Content) {
			return fmt.Errorf("atlas: placement %d content region %+v is outside valid region %+v", p.ID, p.Content, p.Valid)
		}
		if p.Valid.X-p.Expanded.X != a.Alignment ||
			p.Valid.Y-p.Expanded.Y != a.Alignment ||
			p.Expanded.Right()-p.Valid.Right() != a.Alignment ||
			p.Expanded.Bottom()-p.Valid.Bottom() != a.Alignment {
			return fmt.Errorf("atlas: placement %d does not have a uniform level-zero border", p.ID)
		}
		if p.X != p.Expanded.X || p.Y != p.Expanded.Y ||
			p.Width != p.Expanded.Width || p.Height != p.Expanded.Height {
			return fmt.Errorf("atlas: placement %d summary does not match expanded region", p.ID)
		}
		if !insideAtlas(p.Expanded, a.Width, a.Height) {
			return fmt.Errorf("atlas: placement %d expanded region %+v is outside %dx%d", p.ID, p.Expanded, a.Width, a.Height)
		}

		for k := 0; k <= a.MaxMipLevel; k++ {
			m := p.Mips[k]
			if m.Level != k {
				return fmt.Errorf("atlas: placement %d mip region index %d is level %d", p.ID, k, m.Level)
			}
			scale := 1 << uint(k)
			border := a.Alignment / scale
			if p.Content.X%scale != 0 || p.Content.Y%scale != 0 ||
				p.Valid.X%scale != 0 || p.Valid.Y%scale != 0 ||
				p.Valid.Width%scale != 0 || p.Valid.Height%scale != 0 ||
				p.Expanded.X%scale != 0 || p.Expanded.Y%scale != 0 ||
				p.Expanded.Width%scale != 0 || p.Expanded.Height%scale != 0 {
				return fmt.Errorf("atlas: placement %d level %d is not an exact mip subdivision of level zero", p.ID, k)
			}
			want := MipRegion{
				Level: k,
				Content: Region{
					X:      p.Content.X / scale,
					Y:      p.Content.Y / scale,
					Width:  ceilDiv(p.Content.Width, scale),
					Height: ceilDiv(p.Content.Height, scale),
				},
				Valid: Region{
					X:      p.Valid.X / scale,
					Y:      p.Valid.Y / scale,
					Width:  p.Valid.Width / scale,
					Height: p.Valid.Height / scale,
				},
				Expanded: Region{
					X:      p.Expanded.X / scale,
					Y:      p.Expanded.Y / scale,
					Width:  p.Expanded.Width / scale,
					Height: p.Expanded.Height / scale,
				},
				Border: border,
			}
			if m.Content != want.Content || m.Valid != want.Valid || m.Expanded != want.Expanded || m.Border != want.Border {
				return fmt.Errorf("atlas: placement %d level %d regions do not correspond to level zero", p.ID, k)
			}
			if m.Border < 1 {
				return fmt.Errorf("atlas: placement %d level %d border is zero", p.ID, k)
			}
			if !m.Expanded.Contains(m.Valid) {
				return fmt.Errorf("atlas: placement %d level %d valid region outside expanded region", p.ID, k)
			}
			if !m.Valid.Contains(m.Content) {
				return fmt.Errorf("atlas: placement %d level %d content region outside valid region", p.ID, k)
			}
			if m.Valid.X-m.Expanded.X != m.Border ||
				m.Valid.Y-m.Expanded.Y != m.Border ||
				m.Expanded.Right()-m.Valid.Right() != m.Border ||
				m.Expanded.Bottom()-m.Valid.Bottom() != m.Border {
				return fmt.Errorf("atlas: placement %d level %d does not have a uniform border", p.ID, k)
			}
			if !insideAtlas(m.Expanded, a.Width>>uint(k), a.Height>>uint(k)) {
				return fmt.Errorf("atlas: placement %d level %d expanded region is outside the atlas", p.ID, k)
			}
		}
	}

	// Expanded regions are the only regions that are allowed to touch at their
	// boundaries; sharing a pixel would allow a mip tap to read a neighbour.
	for k := 0; k <= a.MaxMipLevel; k++ {
		for i := 0; i < len(a.Placements); i++ {
			for j := i + 1; j < len(a.Placements); j++ {
				ri := a.Placements[i].Mips[k].Expanded
				rj := a.Placements[j].Mips[k].Expanded
				if ri.Overlaps(rj) {
					return fmt.Errorf("atlas: placements %d and %d overlap at level %d: %+v vs %+v",
						a.Placements[i].ID, a.Placements[j].ID, k, ri, rj)
				}
			}
		}
	}
	return nil
}

func isPowerOfTwo(v int) bool {
	return v > 0 && v&(v-1) == 0
}

func insideAtlas(r Region, width, height int) bool {
	return r.X >= 0 && r.Y >= 0 && r.Right() <= width && r.Bottom() <= height
}
