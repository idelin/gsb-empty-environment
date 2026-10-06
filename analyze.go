package contour

// Region is one 4-connected foreground component with its grid statistics.
type Region struct {
	ID        int   // component ID, also the index in Result.Regions
	Area      int   // number of foreground cells
	Perimeter int   // unit edges between the region and background/exterior
	Holes     int   // number of enclosed background components (inner loops)
	Loops     []int // indices into Result.Loops owned by this region
	Min, Max  Point // bounding box in cell coordinates, Max exclusive
}

// BgComponent is one 4-connected background component of the image.
type BgComponent struct {
	ID       int
	Area     int
	Exterior bool  // true if it touches the image border or the outside
	Regions  []int // IDs of adjacent foreground regions, sorted
}

// Result is the full contour analysis of an Image.
type Result struct {
	Image      *Image
	Regions    []*Region
	Loops      []Loop
	Labels     []int // per cell: region ID, or -1 for background
	Background []BgComponent
	bgLabels   []int // per cell: background component ID, or -1 for foreground
}

// BgLabel returns the background component ID of cell (x,y), or -1 if the
// cell is foreground or out of bounds.
func (r *Result) BgLabel(x, y int) int {
	if x < 0 || y < 0 || x >= r.Image.W || y >= r.Image.H {
		return -1
	}
	return r.bgLabels[y*r.Image.W+x]
}

var neigh4 = [4]Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// Analyze labels the 4-connected foreground regions of im (corner-touching
// cells stay separate), traces all boundary loops, and builds the
// flood-fill background model.
func Analyze(im *Image) *Result {
	res := &Result{Image: im}
	res.Labels, res.Regions = labelRegions(im)
	res.Loops = traceLoops(im, res.Labels)
	for i := range res.Loops {
		l := &res.Loops[i]
		reg := res.Regions[l.Region]
		reg.Loops = append(reg.Loops, i)
		if l.Kind == Hole {
			reg.Holes++
		}
	}
	res.bgLabels, res.Background = labelBackground(im, res.Labels)
	return res
}

// labelRegions flood-fills 4-connected foreground components and computes
// area and grid perimeter for each.
func labelRegions(im *Image) ([]int, []*Region) {
	labels := make([]int, im.W*im.H)
	for i := range labels {
		labels[i] = -1
	}
	var regions []*Region
	queue := make([]int, 0, 64)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			idx := y*im.W + x
			if !im.At(x, y) || labels[idx] >= 0 {
				continue
			}
			reg := &Region{ID: len(regions), Min: Point{x, y}, Max: Point{x + 1, y + 1}}
			regions = append(regions, reg)
			labels[idx] = reg.ID
			queue = append(queue[:0], idx)
			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				cx, cy := cur%im.W, cur/im.W
				reg.Area++
				if cx < reg.Min.X {
					reg.Min.X = cx
				}
				if cx+1 > reg.Max.X {
					reg.Max.X = cx + 1
				}
				if cy < reg.Min.Y {
					reg.Min.Y = cy
				}
				if cy+1 > reg.Max.Y {
					reg.Max.Y = cy + 1
				}
				for _, d := range neigh4 {
					nx, ny := cx+d.X, cy+d.Y
					if !im.At(nx, ny) {
						reg.Perimeter++ // background or image exterior
						continue
					}
					ni := ny*im.W + nx
					if labels[ni] < 0 {
						labels[ni] = reg.ID
						queue = append(queue, ni)
					}
				}
			}
		}
	}
	return labels, regions
}

// labelBackground flood-fills 4-connected background components. Components
// touching the image border are exterior; the rest are holes. Each component
// records the foreground regions adjacent to it.
func labelBackground(im *Image, labels []int) ([]int, []BgComponent) {
	bg := make([]int, im.W*im.H)
	for i := range bg {
		bg[i] = -1
	}
	var comps []BgComponent
	queue := make([]int, 0, 64)
	for y := 0; y < im.H; y++ {
		for x := 0; x < im.W; x++ {
			idx := y*im.W + x
			if im.At(x, y) || bg[idx] >= 0 {
				continue
			}
			comp := BgComponent{ID: len(comps)}
			seen := make(map[int]bool)
			bg[idx] = comp.ID
			queue = append(queue[:0], idx)
			for len(queue) > 0 {
				cur := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				cx, cy := cur%im.W, cur/im.W
				comp.Area++
				if cx == 0 || cy == 0 || cx == im.W-1 || cy == im.H-1 {
					comp.Exterior = true
				}
				for _, d := range neigh4 {
					nx, ny := cx+d.X, cy+d.Y
					if nx < 0 || ny < 0 || nx >= im.W || ny >= im.H {
						comp.Exterior = true // open to the outside
						continue
					}
					ni := ny*im.W + nx
					if im.At(nx, ny) {
						if r := labels[ni]; !seen[r] {
							seen[r] = true
							comp.Regions = append(comp.Regions, r)
						}
						continue
					}
					if bg[ni] < 0 {
						bg[ni] = comp.ID
						queue = append(queue, ni)
					}
				}
			}
			// Keep region IDs sorted for deterministic output.
			for i := 1; i < len(comp.Regions); i++ {
				for j := i; j > 0 && comp.Regions[j-1] > comp.Regions[j]; j-- {
					comp.Regions[j-1], comp.Regions[j] = comp.Regions[j], comp.Regions[j-1]
				}
			}
			comps = append(comps, comp)
		}
	}
	return bg, comps
}
