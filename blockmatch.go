package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// Sentinel errors returned by Match.
var (
	ErrInvalidConfig = errors.New("invalid block matching config")
	ErrInvalidFrame  = errors.New("invalid frame")
	ErrNoCandidate   = errors.New("no valid displacement candidate")
)

// Vector is an integer displacement from a reference block to a target block.
type Vector struct {
	DX int
	DY int
}

// L1 returns the Manhattan distance between v and other.
func (v Vector) L1(other Vector) int {
	return absInt(v.DX-other.DX) + absInt(v.DY-other.DY)
}

// Less reports whether v precedes other in the vector list's lexicographic
// order. DX is compared before DY.
func (v Vector) Less(other Vector) bool {
	if v.DX != other.DX {
		return v.DX < other.DX
	}
	return v.DY < other.DY
}

func (v Vector) String() string {
	return fmt.Sprintf("(%d,%d)", v.DX, v.DY)
}

// Window is the inclusive integer displacement search window.
type Window struct {
	MinDX int
	MaxDX int
	MinDY int
	MaxDY int
}

// SymmetricWindow returns a window covering [-radius, radius] on both axes.
// A negative radius produces an invalid window that Match rejects.
func SymmetricWindow(radius int) Window {
	return Window{
		MinDX: -radius,
		MaxDX: radius,
		MinDY: -radius,
		MaxDY: radius,
	}
}

// Config controls block size, displacement window, and smoothing strength.
type Config struct {
	BlockSize int
	Window    Window
	Lambda    float64
}

// NewConfig is a convenience constructor for a symmetric search window.
func NewConfig(blockSize, radius int, lambda float64) Config {
	return Config{
		BlockSize: blockSize,
		Window:    SymmetricWindow(radius),
		Lambda:    lambda,
	}
}

// Frame is a packed 8-bit luma image. Luma must contain exactly Width*Height
// bytes in row-major order.
type Frame struct {
	Luma   []uint8
	Width  int
	Height int
}

// BlockResult contains the selected vector and its two cost components for one
// block. DataError is SAD divided by ValidPixels. SmoothnessCost is Lambda
// multiplied by the L1 distance to the selected vector of the block to the
// left; it is zero for the first block in a row.
type BlockResult struct {
	Row            int
	Col            int
	X              int
	Y              int
	Width          int
	Height         int
	ValidPixels    int
	Vector         Vector
	SAD            uint64
	DataError      float64
	DataCandidates []Vector
	SmoothnessL1   int
	SmoothnessCost float64
}

// Result contains the estimated motion field for all non-overlapping blocks.
// Blocks are stored in row-major order. The vector and cost slices are
// row-major mirrors of Blocks, provided for analysis pipelines that consume
// arrays directly.
type Result struct {
	BlockSize       int
	Cols            int
	Rows            int
	Window          Window
	Lambda          float64
	Blocks          []BlockResult
	Vectors         []Vector
	DataErrors      []float64
	SmoothnessCosts []float64
	DataCost        float64
	SmoothnessCost  float64
	TotalCost       float64
}

// BlockAt returns the result at a block-grid coordinate.
func (r Result) BlockAt(row, col int) (BlockResult, bool) {
	if row < 0 || row >= r.Rows || col < 0 || col >= r.Cols {
		return BlockResult{}, false
	}
	return r.Blocks[row*r.Cols+col], true
}

type candidate struct {
	vector Vector
	sad    uint64
}

type blockCandidates struct {
	x       int
	y       int
	width   int
	height  int
	bestSAD uint64
	list    []candidate
}

// Match estimates one integer motion vector per fixed-size block. For every
// block it first keeps all candidates attaining the minimum SAD, then solves
// each block row globally with a dynamic program minimizing the sum of L1
// distances between adjacent selected vectors. Per-block greedy selection is
// not used. A candidate is valid only when every valid source pixel maps inside
// the target frame. At image edges, partial blocks use only their valid pixels
// in both SAD and DataError normalization.
//
// Objective ties are resolved lexicographically: candidates are ordered by
// (DX, DY), and the lexicographically smallest complete vector sequence wins.
func Match(ref, target Frame, cfg Config) (*Result, error) {
	if err := validateFrame(ref, "reference"); err != nil {
		return nil, err
	}
	if err := validateFrame(target, "target"); err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	cols := (ref.Width-1)/cfg.BlockSize + 1
	rows := (ref.Height-1)/cfg.BlockSize + 1
	result := &Result{
		BlockSize:       cfg.BlockSize,
		Cols:            cols,
		Rows:            rows,
		Window:          cfg.Window,
		Lambda:          cfg.Lambda,
		Blocks:          make([]BlockResult, 0, rows*cols),
		Vectors:         make([]Vector, 0, rows*cols),
		DataErrors:      make([]float64, 0, rows*cols),
		SmoothnessCosts: make([]float64, 0, rows*cols),
	}

	for blockRow := 0; blockRow < rows; blockRow++ {
		y := blockRow * cfg.BlockSize
		height := minInt(cfg.BlockSize, ref.Height-y)
		rowSets := make([][]candidate, cols)

		for blockCol := 0; blockCol < cols; blockCol++ {
			x := blockCol * cfg.BlockSize
			width := minInt(cfg.BlockSize, ref.Width-x)
			bc, err := optimalCandidates(ref, target, cfg.Window, x, y, width, height)
			if err != nil {
				return nil, fmt.Errorf("block row %d col %d: %w", blockRow, blockCol, err)
			}
			rowSets[blockCol] = bc.list

			validPixels := width * height
			dataError := float64(bc.bestSAD) / float64(validPixels)
			result.DataCost += dataError
			result.DataErrors = append(result.DataErrors, dataError)
		}

		chosen, err := solveRow(rowSets, cfg.Lambda)
		if err != nil {
			return nil, fmt.Errorf("block row %d: %w", blockRow, err)
		}

		for blockCol := 0; blockCol < cols; blockCol++ {
			x := blockCol * cfg.BlockSize
			width := minInt(cfg.BlockSize, ref.Width-x)
			height := minInt(cfg.BlockSize, ref.Height-y)
			cands := rowSets[blockCol]
			selected := cands[chosen[blockCol]]
			validPixels := width * height
			dataError := float64(selected.sad) / float64(validPixels)

			smoothL1 := 0
			if blockCol > 0 {
				left := candsAt(rowSets, blockCol-1, chosen[blockCol-1])
				smoothL1 = selected.vector.L1(left.vector)
			}
			smoothCost := cfg.Lambda * float64(smoothL1)
			result.SmoothnessCost += smoothCost
			result.SmoothnessCosts = append(result.SmoothnessCosts, smoothCost)
			result.Vectors = append(result.Vectors, selected.vector)

			allVectors := make([]Vector, len(cands))
			for i := range cands {
				allVectors[i] = cands[i].vector
			}
			result.Blocks = append(result.Blocks, BlockResult{
				Row:            blockRow,
				Col:            blockCol,
				X:              x,
				Y:              y,
				Width:          width,
				Height:         height,
				ValidPixels:    validPixels,
				Vector:         selected.vector,
				SAD:            selected.sad,
				DataError:      dataError,
				DataCandidates: allVectors,
				SmoothnessL1:   smoothL1,
				SmoothnessCost: smoothCost,
			})
		}
	}

	result.TotalCost = result.DataCost + result.SmoothnessCost
	return result, nil
}

// Estimate is an alias for Match.
func Estimate(ref, target Frame, cfg Config) (*Result, error) {
	return Match(ref, target, cfg)
}

// MatchLuma is a convenience wrapper for equally sized packed luma frames.
func MatchLuma(refLuma, targetLuma []uint8, width, height int, cfg Config) (*Result, error) {
	return Match(
		Frame{Luma: refLuma, Width: width, Height: height},
		Frame{Luma: targetLuma, Width: width, Height: height},
		cfg,
	)
}

func validateConfig(cfg Config) error {
	if cfg.BlockSize <= 0 {
		return fmt.Errorf("%w: block size must be positive", ErrInvalidConfig)
	}
	if cfg.Window.MinDX > cfg.Window.MaxDX || cfg.Window.MinDY > cfg.Window.MaxDY {
		return fmt.Errorf("%w: displacement window is empty", ErrInvalidConfig)
	}
	if math.IsNaN(cfg.Lambda) || math.IsInf(cfg.Lambda, 0) || cfg.Lambda < 0 {
		return fmt.Errorf("%w: lambda must be finite and non-negative", ErrInvalidConfig)
	}
	return nil
}

func validateFrame(frame Frame, name string) error {
	if frame.Width <= 0 || frame.Height <= 0 {
		return fmt.Errorf("%w: %s dimensions must be positive", ErrInvalidFrame, name)
	}
	if frame.Width > int(^uint(0)>>1)/frame.Height {
		return fmt.Errorf("%w: %s dimensions overflow", ErrInvalidFrame, name)
	}
	expected := frame.Width * frame.Height
	if len(frame.Luma) != expected {
		return fmt.Errorf(
			"%w: %s luma length %d does not equal width*height %d",
			ErrInvalidFrame, name, len(frame.Luma), expected,
		)
	}
	return nil
}

func optimalCandidates(ref, target Frame, window Window, x, y, width, height int) (blockCandidates, error) {
	bc := blockCandidates{
		x:       x,
		y:       y,
		width:   width,
		height:  height,
		bestSAD: ^uint64(0),
	}

	for dx := window.MinDX; ; dx++ {
		for dy := window.MinDY; ; dy++ {
			left := x + dx
			top := y + dy
			if left >= 0 && top >= 0 &&
				left+width <= target.Width && top+height <= target.Height {
				sad := sadAt(ref, target, x, y, width, height, dx, dy)
				switch {
				case sad < bc.bestSAD:
					bc.bestSAD = sad
					bc.list = bc.list[:0]
					bc.list = append(bc.list, candidate{vector: Vector{DX: dx, DY: dy}, sad: sad})
				case sad == bc.bestSAD:
					bc.list = append(bc.list, candidate{vector: Vector{DX: dx, DY: dy}, sad: sad})
				}
			}
			if dy == window.MaxDY {
				break
			}
		}
		if dx == window.MaxDX {
			break
		}
	}

	if len(bc.list) == 0 {
		return blockCandidates{}, fmt.Errorf(
			"%w: block at (%d,%d) has no displacement fully inside the target",
			ErrNoCandidate, x, y,
		)
	}
	return bc, nil
}

func sadAt(ref, target Frame, x, y, width, height, dx, dy int) uint64 {
	var sum uint64
	for row := 0; row < height; row++ {
		refStart := (y+row)*ref.Width + x
		targetStart := (y+dy+row)*target.Width + x + dx
		for col := 0; col < width; col++ {
			a := int64(ref.Luma[refStart+col])
			b := int64(target.Luma[targetStart+col])
			if a >= b {
				sum += uint64(a - b)
			} else {
				sum += uint64(b - a)
			}
		}
	}
	return sum
}

// solveRow chooses one candidate per block in a row. All candidates for a
// block share the same minimum SAD, so the data term is constant across the
// row; the dynamic program therefore minimizes integer smoothness L1 exactly.
// A lexicographically smallest path breaks ties. For Lambda == 0 every path
// ties, so the first candidate in each block's lexicographic list is selected.
func solveRow(sets [][]candidate, lambda float64) ([]int, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	for col := range sets {
		if len(sets[col]) == 0 {
			return nil, fmt.Errorf("%w: empty candidate set at column %d", ErrNoCandidate, col)
		}
	}
	if lambda == 0 {
		chosen := make([]int, len(sets))
		return chosen, nil
	}

	prevCost := make([]int, len(sets[0]))
	prevPaths := make([][]int, len(sets[0]))
	for i := range prevPaths {
		prevPaths[i] = []int{i}
	}

	for col := 1; col < len(sets); col++ {
		curCost := make([]int, len(sets[col]))
		curPaths := make([][]int, len(sets[col]))
		for curIndex, cur := range sets[col] {
			bestCost := int(^uint(0) >> 1)
			var bestPath []int
			for prevIndex, prev := range sets[col-1] {
				step := cur.vector.L1(prev.vector)
				cost := prevCost[prevIndex] + step
				if cost < bestCost ||
					(cost == bestCost && pathLess(prevPaths[prevIndex], bestPath)) {
					bestCost = cost
					bestPath = prevPaths[prevIndex]
				}
			}
			curCost[curIndex] = bestCost
			nextPath := make([]int, len(bestPath)+1)
			copy(nextPath, bestPath)
			nextPath[len(bestPath)] = curIndex
			curPaths[curIndex] = nextPath
		}
		prevCost = curCost
		prevPaths = curPaths
	}

	bestIndex := 0
	for i := 1; i < len(prevCost); i++ {
		if prevCost[i] < prevCost[bestIndex] ||
			(prevCost[i] == prevCost[bestIndex] && pathLess(prevPaths[i], prevPaths[bestIndex])) {
			bestIndex = i
		}
	}
	return prevPaths[bestIndex], nil
}

func pathLess(a, b []int) bool {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func candsAt(sets [][]candidate, col, index int) candidate {
	return sets[col][index]
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
