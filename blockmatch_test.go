package taskpkg

import (
	"math"
	"reflect"
	"testing"
)

func TestTranslationAndPartialBlocks(t *testing.T) {
	ref := patternedFrame(8, 7)
	const (
		shiftX = 2
		shiftY = 1
	)
	target := shiftedFrame(ref, 3, 3, shiftX, shiftY)
	cfg := Config{
		BlockSize: 3,
		Window: Window{
			MinDX: 1,
			MaxDX: 3,
			MinDY: 0,
			MaxDY: 2,
		},
		Lambda: 2,
	}

	got, err := Match(ref, target, cfg)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Cols != 3 || got.Rows != 3 {
		t.Fatalf("grid = %dx%d, want 3x3", got.Cols, got.Rows)
	}

	for row := 0; row < got.Rows; row++ {
		for col := 0; col < got.Cols; col++ {
			block, ok := got.BlockAt(row, col)
			if !ok {
				t.Fatalf("missing block %d,%d", row, col)
			}
			want := Vector{DX: shiftX, DY: shiftY}
			if block.Vector != want {
				t.Errorf("block %d,%d vector = %v, want %v", row, col, block.Vector, want)
			}
			if block.SAD != 0 || block.DataError != 0 {
				t.Errorf(
					"block %d,%d SAD/error = %d/%v, want 0/0",
					row, col, block.SAD, block.DataError,
				)
			}
			if block.ValidPixels != block.Width*block.Height {
				t.Errorf("block %d,%d valid pixels mismatch", row, col)
			}
		}
	}

	last := got.Blocks[len(got.Blocks)-1]
	if last.Width != 2 || last.Height != 1 || last.ValidPixels != 2 {
		t.Fatalf("bottom-right partial block = %dx%d (%d px), want 2x1 (2 px)",
			last.Width, last.Height, last.ValidPixels)
	}
}

func TestPartialBlockErrorUsesValidPixels(t *testing.T) {
	ref := uniformFrame(5, 5, 10)
	target := uniformFrame(5, 5, 20)
	cfg := Config{
		BlockSize: 4,
		Window:    Window{},
		Lambda:    1,
	}

	got, err := Match(ref, target, cfg)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Cols != 2 || got.Rows != 2 {
		t.Fatalf("grid = %dx%d, want 2x2", got.Cols, got.Rows)
	}
	for _, block := range got.Blocks {
		if block.ValidPixels != block.Width*block.Height {
			t.Errorf("block %d,%d valid pixels mismatch", block.Row, block.Col)
		}
		if math.Abs(block.DataError-10) > 1e-12 {
			t.Errorf(
				"block %d,%d error = %v, want 10 for %dx%d valid block",
				block.Row, block.Col, block.DataError,
				block.Width, block.Height,
			)
		}
	}
}

func TestOutOfBoundsCandidatesAreExcluded(t *testing.T) {
	ref := patternedFrame(4, 4)
	target := patternedFrame(4, 4)
	cfg := Config{
		BlockSize: 2,
		Window: Window{
			MinDX: -1,
			MaxDX: 1,
			MinDY: -1,
			MaxDY: 1,
		},
		Lambda: 1,
	}

	got, err := Match(ref, target, cfg)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	for _, block := range got.Blocks {
		for _, v := range block.DataCandidates {
			left := block.X + v.DX
			top := block.Y + v.DY
			if left < 0 || top < 0 ||
				left+block.Width > target.Width ||
				top+block.Height > target.Height {
				t.Errorf(
					"block %d,%d contains out-of-bounds candidate %v",
					block.Row, block.Col, v,
				)
			}
		}
	}
}

func TestRepeatedStripesAreStableAndLexicographic(t *testing.T) {
	ref := stripedFrame(12, 2, 2)
	target := stripedFrame(16, 2, 2)
	cfg := Config{
		BlockSize: 2,
		Window: Window{
			MinDX: 2,
			MaxDX: 4,
			MinDY: 0,
			MaxDY: 0,
		},
		Lambda: 3,
	}

	got, err := Match(ref, target, cfg)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got.Rows != 1 {
		t.Fatalf("rows = %d, want 1", got.Rows)
	}
	for col, block := range got.Blocks {
		wantCandidates := []Vector{{DX: 2, DY: 0}, {DX: 4, DY: 0}}
		if !reflect.DeepEqual(block.DataCandidates, wantCandidates) {
			t.Fatalf("block col %d candidates = %v, want %v",
				col, block.DataCandidates, wantCandidates)
		}
		if block.Vector != (Vector{DX: 2, DY: 0}) {
			t.Errorf("block col %d vector = %v, want lexicographic (2,0)",
				col, block.Vector)
		}
		if block.SAD != 0 || block.SmoothnessL1 != 0 {
			t.Errorf("block col %d SAD/smooth L1 = %d/%d, want 0/0",
				col, block.SAD, block.SmoothnessL1)
		}
	}
	if got.SmoothnessCost != 0 {
		t.Errorf("total smoothness = %v, want 0", got.SmoothnessCost)
	}
}

func TestExhaustiveRowsMatchBruteForce(t *testing.T) {
	translationRef := patternedFrame(9, 7)
	translationTarget := shiftedFrame(translationRef, 3, 3, 2, 1)

	occlusionRef := patternedFrame(9, 7)
	occlusionTarget := shiftedFrame(occlusionRef, 3, 3, 2, 1)
	occludeRect(occlusionTarget, 5, 3, 3, 3, 0)

	tests := []struct {
		name   string
		ref    Frame
		target Frame
		cfg    Config
	}{
		{
			name:   "translation",
			ref:    translationRef,
			target: translationTarget,
			cfg: Config{
				BlockSize: 3,
				Window: Window{
					MinDX: 1,
					MaxDX: 3,
					MinDY: 0,
					MaxDY: 2,
				},
				Lambda: 1.5,
			},
		},
		{
			name:   "occlusion",
			ref:    occlusionRef,
			target: occlusionTarget,
			cfg: Config{
				BlockSize: 3,
				Window: Window{
					MinDX: 1,
					MaxDX: 3,
					MinDY: 0,
					MaxDY: 2,
				},
				Lambda: 1.5,
			},
		},
		{
			name:   "repeated_stripes",
			ref:    stripedFrame(12, 2, 2),
			target: stripedFrame(16, 2, 2),
			cfg: Config{
				BlockSize: 2,
				Window: Window{
					MinDX: 2,
					MaxDX: 4,
					MinDY: 0,
					MaxDY: 0,
				},
				Lambda: 1.5,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(tc.ref, tc.target, tc.cfg)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}

			for row := 0; row < got.Rows; row++ {
				expectedSets := make([][]Vector, got.Cols)
				for col := 0; col < got.Cols; col++ {
					block, ok := got.BlockAt(row, col)
					if !ok {
						t.Fatalf("missing block %d,%d", row, col)
					}
					vectors, sad := bruteOptimalCandidates(
						tc.ref, tc.target, tc.cfg.Window,
						block.X, block.Y, block.Width, block.Height,
					)
					expectedSets[col] = vectors
					if !reflect.DeepEqual(block.DataCandidates, vectors) {
						t.Errorf(
							"block %d,%d candidates = %v, brute force = %v",
							row, col, block.DataCandidates, vectors,
						)
					}
					if block.SAD != sad {
						t.Errorf("block %d,%d SAD = %d, brute force = %d",
							row, col, block.SAD, sad)
					}
					wantError := float64(sad) / float64(block.ValidPixels)
					if math.Abs(block.DataError-wantError) > 1e-12 {
						t.Errorf("block %d,%d data error = %v, brute force = %v",
							row, col, block.DataError, wantError)
					}
				}

				wantVectors := bruteSolveRow(expectedSets, tc.cfg.Lambda)
				for col := 0; col < got.Cols; col++ {
					block, _ := got.BlockAt(row, col)
					if block.Vector != wantVectors[col] {
						t.Errorf(
							"block %d,%d vector = %v, exhaustive %v",
							row, col, block.Vector, wantVectors[col],
						)
					}
				}
				wantSmooth := bruteRowSmoothness(wantVectors)
				var gotSmooth int
				for col := 0; col < got.Cols; col++ {
					block, _ := got.BlockAt(row, col)
					gotSmooth += block.SmoothnessL1
				}
				if gotSmooth != wantSmooth {
					t.Errorf("row %d smooth L1 = %d, exhaustive = %d",
						row, gotSmooth, wantSmooth)
				}
			}
		})
	}
}

func TestRowSolverBeatsLexicographicGreedy(t *testing.T) {
	sets := [][]candidate{
		{
			{vector: Vector{DX: -2, DY: 0}},
			{vector: Vector{DX: 0, DY: 0}},
		},
		{
			{vector: Vector{DX: 0, DY: 0}},
			{vector: Vector{DX: 2, DY: 0}},
		},
		{
			{vector: Vector{DX: 0, DY: 0}},
			{vector: Vector{DX: 2, DY: 0}},
		},
	}

	got, err := solveRow(sets, 1)
	if err != nil {
		t.Fatalf("solveRow: %v", err)
	}
	want := []int{1, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("solveRow = %v, want globally smooth %v (greedy would choose [0 0 0])",
			got, want)
	}
}

func TestRowSolverLexicographicTie(t *testing.T) {
	sets := [][]candidate{
		{
			{vector: Vector{DX: -1, DY: 0}},
			{vector: Vector{DX: 1, DY: 0}},
		},
		{
			{vector: Vector{DX: -1, DY: 0}},
			{vector: Vector{DX: 1, DY: 0}},
		},
	}

	got, err := solveRow(sets, 2)
	if err != nil {
		t.Fatalf("solveRow: %v", err)
	}
	want := []int{0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("solveRow = %v, want lexicographic %v", got, want)
	}
}

func TestRowSolverLambdaZeroChoosesLexicographicFirst(t *testing.T) {
	sets := [][]candidate{
		{
			{vector: Vector{DX: -1, DY: 0}},
			{vector: Vector{DX: 5, DY: 0}},
		},
		{
			{vector: Vector{DX: 4, DY: 0}},
			{vector: Vector{DX: 4, DY: 3}},
		},
	}

	got, err := solveRow(sets, 0)
	if err != nil {
		t.Fatalf("solveRow: %v", err)
	}
	want := []int{0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("solveRow = %v, want lexicographic %v", got, want)
	}
}

func TestRowSolverMatchesBruteForceOnCandidateSets(t *testing.T) {
	pools := [][]Vector{
		{{DX: -2, DY: 0}, {DX: 0, DY: 0}, {DX: 1, DY: 1}},
		{{DX: 0, DY: 0}, {DX: 2, DY: 0}, {DX: 2, DY: 1}},
		{{DX: -1, DY: 1}, {DX: 0, DY: 0}, {DX: 3, DY: 0}},
	}
	for keepA := 1; keepA <= len(pools[0]); keepA++ {
		for keepB := 1; keepB <= len(pools[1]); keepB++ {
			for keepC := 1; keepC <= len(pools[2]); keepC++ {
				keep := []int{keepA, keepB, keepC}
				sets := make([][]candidate, len(pools))
				for i := range pools {
					for j := 0; j < keep[i]; j++ {
						sets[i] = append(sets[i], candidate{vector: pools[i][j]})
					}
				}
				got, err := solveRow(sets, 2.5)
				if err != nil {
					t.Fatalf("solveRow: %v", err)
				}
				gotVectors := make([]Vector, len(got))
				for i := range got {
					gotVectors[i] = sets[i][got[i]].vector
				}
				wantVectors := bruteSolveRow(flattenVectors(sets), 2.5)
				if !reflect.DeepEqual(gotVectors, wantVectors) {
					t.Fatalf("solveRow = %v, brute force = %v", gotVectors, wantVectors)
				}
			}
		}
	}
}

func bruteOptimalCandidates(
	ref, target Frame,
	window Window,
	x, y, width, height int,
) ([]Vector, uint64) {
	var best uint64 = ^uint64(0)
	var vectors []Vector
	for dx := window.MinDX; dx <= window.MaxDX; dx++ {
		for dy := window.MinDY; dy <= window.MaxDY; dy++ {
			left := x + dx
			top := y + dy
			if left < 0 || top < 0 ||
				left+width > target.Width || top+height > target.Height {
				continue
			}
			sad := bruteSAD(ref, target, x, y, width, height, dx, dy)
			if sad < best {
				best = sad
				vectors = vectors[:0]
				vectors = append(vectors, Vector{DX: dx, DY: dy})
			} else if sad == best {
				vectors = append(vectors, Vector{DX: dx, DY: dy})
			}
		}
	}
	return vectors, best
}

func bruteSAD(ref, target Frame, x, y, width, height, dx, dy int) uint64 {
	var sum uint64
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			a := int64(ref.Luma[(y+row)*ref.Width+x+col])
			b := int64(target.Luma[(y+dy+row)*target.Width+x+dx+col])
			if a < b {
				sum += uint64(b - a)
			} else {
				sum += uint64(a - b)
			}
		}
	}
	return sum
}

func bruteSolveRow(sets [][]Vector, lambda float64) []Vector {
	if lambda == 0 {
		result := make([]Vector, len(sets))
		for col := range sets {
			result[col] = sets[col][0]
		}
		return result
	}

	var best []Vector
	bestSmooth := int(^uint(0) >> 1)
	path := make([]Vector, len(sets))

	var visit func(col, smooth int)
	visit = func(col, smooth int) {
		if col == len(sets) {
			if smooth < bestSmooth ||
				(smooth == bestSmooth && lexVectorPathLess(path, best)) {
				bestSmooth = smooth
				best = append([]Vector(nil), path...)
			}
			return
		}
		for _, vector := range sets[col] {
			nextSmooth := smooth
			if col > 0 {
				nextSmooth += vector.L1(path[col-1])
			}
			path[col] = vector
			visit(col+1, nextSmooth)
		}
	}
	visit(0, 0)
	return best
}

func bruteRowSmoothness(vectors []Vector) int {
	var sum int
	for i := 1; i < len(vectors); i++ {
		sum += vectors[i].L1(vectors[i-1])
	}
	return sum
}

func lexVectorPathLess(a, b []Vector) bool {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return a[i].Less(b[i])
		}
	}
	return len(a) < len(b)
}

func flattenVectors(sets [][]candidate) [][]Vector {
	result := make([][]Vector, len(sets))
	for i := range sets {
		result[i] = make([]Vector, len(sets[i]))
		for j := range sets[i] {
			result[i][j] = sets[i][j].vector
		}
	}
	return result
}

func patternedFrame(width, height int) Frame {
	frame := Frame{
		Luma:   make([]uint8, width*height),
		Width:  width,
		Height: height,
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			frame.Luma[y*width+x] = uint8((x*47 + y*83 + x*y*19 + 11) % 251)
		}
	}
	return frame
}

func shiftedFrame(ref Frame, padX, padY, shiftX, shiftY int) Frame {
	target := Frame{
		Luma:   make([]uint8, (ref.Width+padX)*(ref.Height+padY)),
		Width:  ref.Width + padX,
		Height: ref.Height + padY,
	}
	for y := 0; y < ref.Height; y++ {
		for x := 0; x < ref.Width; x++ {
			target.Luma[(y+shiftY)*target.Width+x+shiftX] = ref.Luma[y*ref.Width+x]
		}
	}
	return target
}

func uniformFrame(width, height int, value uint8) Frame {
	frame := Frame{
		Luma:   make([]uint8, width*height),
		Width:  width,
		Height: height,
	}
	for i := range frame.Luma {
		frame.Luma[i] = value
	}
	return frame
}

func stripedFrame(width, height, period int) Frame {
	frame := Frame{
		Luma:   make([]uint8, width*height),
		Width:  width,
		Height: height,
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x%period == 0 {
				frame.Luma[y*width+x] = 30
			} else {
				frame.Luma[y*width+x] = 210
			}
		}
	}
	return frame
}

func occludeRect(frame Frame, x, y, width, height int, value uint8) {
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			frame.Luma[row*frame.Width+col] = value
		}
	}
}
