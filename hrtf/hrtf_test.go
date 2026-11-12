package hrtf

import (
	"math"
	"math/rand"
	"testing"
)

// Octahedron vertex indices:
//
//	0:+X 1:-X 2:+Y 3:-Y 4:+Z 5:-Z
func octaDirs() []Vec3 {
	return []Vec3{
		{1, 0, 0}, {-1, 0, 0},
		{0, 1, 0}, {0, -1, 0},
		{0, 0, 1}, {0, 0, -1},
	}
}

func octaFaces() []Face {
	return []Face{
		{ID: 0, A: 0, B: 2, C: 4}, // +X +Y +Z
		{ID: 1, A: 2, B: 1, C: 4},
		{ID: 2, A: 1, B: 3, C: 4},
		{ID: 3, A: 3, B: 0, C: 4},
		{ID: 4, A: 2, B: 0, C: 5}, // +Y +X -Z
		{ID: 5, A: 1, B: 2, C: 5},
		{ID: 6, A: 3, B: 1, C: 5},
		{ID: 7, A: 0, B: 3, C: 5},
	}
}

func makeOctaMesh(filters [6][2]Filter) (*Mesh, error) {
	dirs := octaDirs()
	verts := make([]Vertex, 6)
	for i := range verts {
		verts[i] = Vertex{Dir: dirs[i], Left: filters[i][0], Right: filters[i][1]}
	}
	return NewMesh(verts, octaFaces())
}

func deltaFilter(delay int) Filter {
	return Filter{FIR: []float64{1, 0, 0, 0}, Delay: delay}
}

func constFilter(v float64, n int) Filter {
	f := make([]float64, n)
	for i := range f {
		f[i] = v
	}
	return Filter{FIR: f}
}

func rampFilter(vi int, ear Channel, delay int) Filter {
	f := make([]float64, 4)
	sign := 1.0
	if ear == Right {
		sign = -1.0
	}
	for j := range f {
		f[j] = sign * (float64(vi+1) + 0.1*float64(j))
	}
	return Filter{FIR: f, Delay: delay}
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func sliceApprox(a, b []float64, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !approx(a[i], b[i], tol) {
			return false
		}
	}
	return true
}

func TestNewMeshValidation(t *testing.T) {
	good := [6][2]Filter{}
	for i := range good {
		good[i] = [2]Filter{deltaFilter(i), deltaFilter(i + 1)}
	}
	if _, err := makeOctaMesh(good); err != nil {
		t.Fatalf("valid mesh rejected: %v", err)
	}

	dirs := octaDirs()
	faces := octaFaces()
	base := func() []Vertex {
		vs := make([]Vertex, 6)
		for i := range vs {
			vs[i] = Vertex{Dir: dirs[i], Left: deltaFilter(0), Right: deltaFilter(0)}
		}
		return vs
	}

	if _, err := NewMesh(nil, faces); err == nil {
		t.Fatal("expected error for no vertices")
	}
	if _, err := NewMesh(base(), nil); err == nil {
		t.Fatal("expected error for no faces")
	}

	t.Run("zero direction", func(t *testing.T) {
		vs := base()
		vs[2].Dir = Vec3{}
		if _, err := NewMesh(vs, faces); err == nil {
			t.Fatal("expected error for zero direction")
		}
	})

	t.Run("empty FIR", func(t *testing.T) {
		vs := base()
		vs[1].Left.FIR = nil
		if _, err := NewMesh(vs, faces); err == nil {
			t.Fatal("expected error for empty FIR")
		}
	})

	t.Run("unequal ear length", func(t *testing.T) {
		vs := base()
		vs[1].Right.FIR = []float64{1, 0}
		if _, err := NewMesh(vs, faces); err == nil {
			t.Fatal("expected error for mismatched ear FIR length")
		}
	})

	t.Run("unequal global length", func(t *testing.T) {
		vs := base()
		short := Filter{FIR: []float64{1, 0}, Delay: 0}
		vs[3].Left = short
		vs[3].Right = short
		if _, err := NewMesh(vs, faces); err == nil {
			t.Fatal("expected error for global FIR length mismatch")
		}
	})

	t.Run("negative delay", func(t *testing.T) {
		vs := base()
		vs[0].Left.Delay = -1
		if _, err := NewMesh(vs, faces); err == nil {
			t.Fatal("expected error for negative delay")
		}
	})

	t.Run("bad face index", func(t *testing.T) {
		bad := append([]Face(nil), faces...)
		bad[0].A = 99
		if _, err := NewMesh(base(), bad); err == nil {
			t.Fatal("expected error for out-of-range face index")
		}
		bad = append([]Face(nil), faces...)
		bad[0].B = bad[0].A
		if _, err := NewMesh(base(), bad); err == nil {
			t.Fatal("expected error for degenerate face")
		}
	})

	t.Run("duplicate face ID", func(t *testing.T) {
		bad := append([]Face(nil), faces...)
		bad[5].ID = bad[0].ID
		if _, err := NewMesh(base(), bad); err == nil {
			t.Fatal("expected error for duplicate face ID")
		}
	})
}

func TestLocateGeometry(t *testing.T) {
	filters := [6][2]Filter{}
	for i := range filters {
		filters[i] = [2]Filter{deltaFilter(i), deltaFilter(6 - i)}
	}
	mesh, err := makeOctaMesh(filters)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)

	t.Run("vertex resolves to fixed face with unit weight", func(t *testing.T) {
		loc, err := eng.Locate(Vec3{0, 0, 1})
		if err != nil {
			t.Fatal(err)
		}
		if loc.Face.ID != 0 {
			t.Fatalf("vertex +Z: expected smallest face ID 0, got %d", loc.Face.ID)
		}
		if !approx(loc.C, 1, 1e-12) || !approx(loc.A+loc.B, 0, 1e-12) {
			t.Fatalf("vertex +Z weights = %v,%v,%v", loc.A, loc.B, loc.C)
		}

		loc, _ = eng.Locate(Vec3{1, 0, 0})
		if loc.Face.ID != 0 || !approx(loc.A, 1, 1e-12) {
			t.Fatalf("vertex +X: face %d weight A=%v", loc.Face.ID, loc.A)
		}
	})

	t.Run("shared edge has fixed face and half weights", func(t *testing.T) {
		edge := Vec3{1, 1, 0}.normalize()
		loc, err := eng.Locate(edge)
		if err != nil {
			t.Fatal(err)
		}
		if loc.Face.ID != 0 {
			t.Fatalf("edge expected face 0, got %d", loc.Face.ID)
		}
		if !approx(loc.A, 0.5, 1e-12) || !approx(loc.B, 0.5, 1e-12) ||
			!approx(loc.C, 0, 1e-12) {
			t.Fatalf("edge weights = %v,%v,%v", loc.A, loc.B, loc.C)
		}
	})

	t.Run("face interior gives equal barycentric weights", func(t *testing.T) {
		loc, err := eng.Locate(Vec3{1, 1, 1})
		if err != nil {
			t.Fatal(err)
		}
		if loc.Face.ID != 0 {
			t.Fatalf("centroid ray expected face 0, got %d", loc.Face.ID)
		}
		for i, w := range loc.Weights() {
			if !approx(w, 1.0/3.0, 1e-12) {
				t.Fatalf("weight %d = %v, want 1/3", i, w)
			}
		}
	})

	t.Run("opposite sides of edge select different faces", func(t *testing.T) {
		edge := Vec3{1, 1, 0}.normalize()
		up := edge.add(Vec3{0, 0, 0.3}).normalize()
		down := edge.sub(Vec3{0, 0, 0.3}).normalize()
		locUp, err := eng.Locate(up)
		if err != nil {
			t.Fatal(err)
		}
		locDown, err := eng.Locate(down)
		if err != nil {
			t.Fatal(err)
		}
		if locUp.Face.ID != 0 || locDown.Face.ID != 4 {
			t.Fatalf("side faces = %d, %d, want 0 and 4", locUp.Face.ID, locDown.Face.ID)
		}
	})

	t.Run("zero direction rejected", func(t *testing.T) {
		if _, err := eng.Locate(Vec3{}); err == nil {
			t.Fatal("expected error for zero query direction")
		}
	})
}

func TestBoundaryDeterminismIndependentOfFaceOrder(t *testing.T) {
	filters := [6][2]Filter{}
	for i := range filters {
		filters[i] = [2]Filter{deltaFilter(i), deltaFilter(i)}
	}
	dirs := octaDirs()
	verts := make([]Vertex, 6)
	for i := range verts {
		verts[i] = Vertex{Dir: dirs[i], Left: filters[i][0], Right: filters[i][1]}
	}

	shuffled := append([]Face(nil), octaFaces()...)
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	m1, err := NewMesh(verts, octaFaces())
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewMesh(verts, shuffled)
	if err != nil {
		t.Fatal(err)
	}
	edge := Vec3{1, 1, 0}.normalize()
	l1, err := m1.Locate(edge)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := m2.Locate(edge)
	if err != nil {
		t.Fatal(err)
	}
	if l1.Face.ID != l2.Face.ID || !sliceApprox(l1.Weights(), l2.Weights(), 1e-12) {
		t.Fatalf("boundary selection depends on face order: %+v vs %+v", l1, l2)
	}
}

func TestConstantFIR(t *testing.T) {
	const val = 0.5
	filters := [6][2]Filter{}
	for i := range filters {
		cf := constFilter(val, 4)
		filters[i] = [2]Filter{
			{FIR: cf.FIR, Delay: i},
			{FIR: cf.FIR, Delay: i + 2},
		}
	}
	mesh, err := makeOctaMesh(filters)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)

	l, r, err := eng.Interpolate(Vec3{1, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string][]float64{"left": l.FIR, "right": r.FIR} {
		for _, x := range f {
			if !approx(x, val, 1e-12) {
				t.Fatalf("constant %s FIR interpolated to %v", name, f)
			}
		}
	}

	// Fractional delay only redistributes each tap onto two samples, so the
	// total impulse energy must be preserved.
	resp := Response{FIR: constFilter(val, 4).FIR, Delay: 2.3}
	imp := resp.Impulse()
	var sumFIR, sumImp float64
	for _, x := range resp.FIR {
		sumFIR += x
	}
	for _, x := range imp {
		sumImp += x
	}
	if !approx(sumImp, sumFIR, 1e-12) {
		t.Fatalf("energy not preserved across fractional delay: %v vs %v", sumImp, sumFIR)
	}
}

func TestVertexReproduction(t *testing.T) {
	filters := [6][2]Filter{}
	for i := range filters {
		filters[i] = [2]Filter{rampFilter(i, Left, i), rampFilter(i, Right, 10-i)}
	}
	mesh, err := makeOctaMesh(filters)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)

	for vi, dir := range octaDirs() {
		l, r, err := eng.Interpolate(dir)
		if err != nil {
			t.Fatal(err)
		}
		wantL := filters[vi][0]
		wantR := filters[vi][1]
		if !sliceApprox(l.FIR, wantL.FIR, 1e-12) {
			t.Fatalf("vertex %d left FIR = %v, want %v", vi, l.FIR, wantL.FIR)
		}
		if !sliceApprox(r.FIR, wantR.FIR, 1e-12) {
			t.Fatalf("vertex %d right FIR = %v, want %v", vi, r.FIR, wantR.FIR)
		}
		if !approx(l.Delay, float64(wantL.Delay), 1e-12) {
			t.Fatalf("vertex %d left delay = %v, want %d", vi, l.Delay, wantL.Delay)
		}
		if !approx(r.Delay, float64(wantR.Delay), 1e-12) {
			t.Fatalf("vertex %d right delay = %v, want %d", vi, r.Delay, wantR.Delay)
		}
	}
}

func TestFractionalDelayLinearInterpolation(t *testing.T) {
	// Integer delay: single tap lands exactly on index d.
	intResp := Response{FIR: []float64{1, 0, 0}, Delay: 2}
	intImp := intResp.Impulse()
	wantInt := []float64{0, 0, 1, 0, 0, 0}
	if !sliceApprox(intImp, wantInt, 1e-12) {
		t.Fatalf("integer delay impulse = %v, want %v", intImp, wantInt)
	}

	// Fractional delay d=2.3: tap mass splits 0.7/0.3 across adjacent samples.
	fracResp := Response{FIR: []float64{1, 0, 0}, Delay: 2.3}
	fracImp := fracResp.Impulse()
	wantFrac := []float64{0, 0, 0.7, 0.3, 0, 0}
	if !sliceApprox(fracImp, wantFrac, 1e-12) {
		t.Fatalf("fractional delay impulse = %v, want %v", fracImp, wantFrac)
	}

	// Endpoints frac=0 and frac=1 must coincide with integer shifts.
	atOne := Response{FIR: []float64{1, 0, 0}, Delay: 3.0}.Impulse()
	if !sliceApprox(atOne, []float64{0, 0, 0, 1, 0, 0, 0}, 1e-12) {
		t.Fatalf("delay 3.0 impulse = %v", atOne)
	}
}

// peakDelay reports the center of mass of an impulse, giving a sub-sample
// arrival-time estimate even when the energy is split by fractional delay.
func peakDelay(imp []float64) float64 {
	var num, den float64
	for i, x := range imp {
		num += float64(i) * x
		den += x
	}
	if den == 0 {
		return math.NaN()
	}
	return num / den
}

func nonTrivialFilters() [6][2]Filter {
	filters := [6][2]Filter{}
	for i := range filters {
		f := make([]float64, 4)
		for j := range f {
			f[j] = float64(i+1) + 0.25*float64(j)
		}
		filters[i] = [2]Filter{
			{FIR: append([]float64(nil), f...), Delay: i},
			{FIR: append([]float64(nil), f...), Delay: (6 - i) % 6},
		}
	}
	return filters
}

// edgeResponses evaluates the same direction using the two triangles sharing
// the +X/+Y equator edge, with matching barycentric weights (a,b,0).
func edgeResponses(eng *Engine, a, b float64) (face0, face4 Response) {
	loc0 := Location{
		Face:     Face{ID: 0, A: 0, B: 2, C: 4},
		A:        a,
		B:        b,
		C:        0,
		Vertices: [3]int{0, 2, 4},
	}
	loc4 := Location{
		Face:     Face{ID: 4, A: 2, B: 0, C: 5},
		A:        b,
		B:        a,
		C:        0,
		Vertices: [3]int{2, 0, 5},
	}
	return eng.InterpolateEar(Left, loc0), eng.InterpolateEar(Left, loc4)
}

func TestCrossEdgeContinuity(t *testing.T) {
	mesh, err := makeOctaMesh(nonTrivialFilters())
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)

	// Exact agreement on the shared edge regardless of which adjacent face's
	// ID wins the boundary rule.
	for _, a := range []float64{0.25, 0.5, 0.75} {
		r0, r4 := edgeResponses(eng, a, 1-a)
		if !sliceApprox(r0.FIR, r4.FIR, 1e-12) {
			t.Fatalf("edge FIR mismatch at a=%v: %v vs %v", a, r0.FIR, r4.FIR)
		}
		if !approx(r0.Delay, r4.Delay, 1e-12) {
			t.Fatalf("edge delay mismatch at a=%v: %v vs %v", a, r0.Delay, r4.Delay)
		}
		if !sliceApprox(r0.Impulse(), r4.Impulse(), 1e-12) {
			t.Fatalf("edge impulse mismatch at a=%v", a)
		}
	}

	// Sweep a ray across the edge; both FIR and impulse must move continuously.
	const steps = 2001
	edge := Vec3{1, 1, 0}.normalize()
	var prevFIR, prevImp []float64
	var prevDelay float64
	maxJump := 0.0
	for k := 0; k <= steps; k++ {
		z := -0.5 + float64(k)/steps
		dir := edge.add(Vec3{0, 0, z}).normalize()
		l, _, err := eng.Interpolate(dir)
		if err != nil {
			t.Fatal(err)
		}
		imp := l.Impulse()
		if k > 0 {
			if math.Abs(l.Delay-prevDelay) > 0.05 {
				t.Fatalf("delay jump at step %d: %v -> %v", k, prevDelay, l.Delay)
			}
			jump := 0.0
			for j := range l.FIR {
				jump = math.Max(jump, math.Abs(l.FIR[j]-prevFIR[j]))
			}
			// Impulse length grows with delay; compare the shared span and
			// account for any newly appearing tail samples.
			shared := len(imp)
			if len(prevImp) < shared {
				shared = len(prevImp)
			}
			for j := 0; j < shared; j++ {
				jump = math.Max(jump, math.Abs(imp[j]-prevImp[j]))
			}
			for j := shared; j < len(imp); j++ {
				jump = math.Max(jump, math.Abs(imp[j]))
			}
			for j := shared; j < len(prevImp); j++ {
				jump = math.Max(jump, math.Abs(prevImp[j]))
			}
			maxJump = math.Max(maxJump, jump)
		}
		prevFIR = append(prevFIR[:0], l.FIR...)
		prevImp = append(prevImp[:0], imp...)
		prevDelay = l.Delay
	}
	// Per-step variation of a smooth 2001-step sweep is tiny; an O(1) face
	// switch artifact would dwarf this bound.
	if maxJump > 0.05 {
		t.Fatalf("discontinuity across edge, max per-step jump %v", maxJump)
	}
}

func TestInterauralTimeDifference(t *testing.T) {
	// Identical delta FIRs; only the explicit integer delays differ per ear.
	filters := [6][2]Filter{}
	for i := range filters {
		filters[i] = [2]Filter{
			{FIR: []float64{1, 0, 0, 0}, Delay: 10 + i},
			{FIR: []float64{1, 0, 0, 0}, Delay: 2 + 2*i},
		}
	}
	mesh, err := makeOctaMesh(filters)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)

	loc, err := eng.Locate(Vec3{0.3, -0.7, 0.4})
	if err != nil {
		t.Fatal(err)
	}
	l, r, err := eng.Interpolate(Vec3{0.3, -0.7, 0.4})
	if err != nil {
		t.Fatal(err)
	}

	wantDelay := func(ear Channel) float64 {
		var d float64
		for k, vi := range loc.Vertices {
			w := loc.Weights()[k]
			d += w * float64(mesh.vertices[vi].channel(ear).Delay)
		}
		return d
	}
	if !approx(l.Delay, wantDelay(Left), 1e-12) || !approx(r.Delay, wantDelay(Right), 1e-12) {
		t.Fatalf("delay mismatch: l=%v (want %v), r=%v (want %v)",
			l.Delay, wantDelay(Left), r.Delay, wantDelay(Right))
	}

	// The arrival-time difference of the materialized impulses equals the
	// difference of the interpolated delays.
	itd := peakDelay(l.Impulse()) - peakDelay(r.Impulse())
	if !approx(itd, l.Delay-r.Delay, 1e-12) {
		t.Fatalf("measured ITD %v != delay difference %v", itd, l.Delay-r.Delay)
	}

	// ITD stays continuous while sweeping across the shared edge.
	const steps = 1000
	edge := Vec3{1, 1, 0}.normalize()
	var prevITD float64
	maxITDJump := 0.0
	for k := 0; k <= steps; k++ {
		z := -0.5 + float64(k)/steps
		l, r, err := eng.Interpolate(edge.add(Vec3{0, 0, z}).normalize())
		if err != nil {
			t.Fatal(err)
		}
		cur := l.Delay - r.Delay
		if k > 0 {
			maxITDJump = math.Max(maxITDJump, math.Abs(cur-prevITD))
		}
		prevITD = cur
	}
	if maxITDJump > 0.05 {
		t.Fatalf("ITD discontinuity across edge, max jump %v", maxITDJump)
	}
}

// referenceConvolve runs the same linear-fractional-delay convolution with a
// plain growing history slice, independently of the ring-buffer Convolver.
func referenceConvolve(input []float64, resp Response) []float64 {
	base := math.Floor(resp.Delay)
	d := int(base)
	frac := resp.Delay - base
	hist := func(n, k int) float64 {
		idx := n - k
		if idx < 0 {
			return 0
		}
		return input[idx]
	}
	out := make([]float64, len(input))
	for n := range input {
		var acc float64
		for j, h := range resp.FIR {
			k := d + j
			near := hist(n, k)
			far := hist(n, k+1)
			acc += h * ((1-frac)*near + frac*far)
		}
		out[n] = acc
	}
	return out
}

func TestConvolverMatchesReference(t *testing.T) {
	mesh, err := makeOctaMesh(nonTrivialFilters())
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)
	lc := eng.NewConvolver(Left)
	rc := eng.NewConvolver(Right)

	rng := rand.New(rand.NewSource(42))
	input := make([]float64, 128)
	for i := range input {
		input[i] = rng.NormFloat64()
	}

	// Switch filters at block boundaries, including a fractional delay.
	dirs := []Vec3{
		{1, 1, 1},
		Vec3{1, 1, 0.2}.normalize(),
		Vec3{-0.4, 0.9, 0.1}.normalize(),
		Vec3{0.2, 0.2, -1}.normalize(),
	}
	block := 32
	var gotL, gotR, refL, refR []float64
	histL := []float64{}
	histR := []float64{}
	for b, dir := range dirs {
		if err := eng.Update(dir, lc, rc); err != nil {
			t.Fatal(err)
		}
		seg := input[b*block : (b+1)*block]
		gotL = append(gotL, lc.ProcessBlock(seg)...)
		gotR = append(gotR, rc.ProcessBlock(seg)...)

		l, r, err := eng.Interpolate(dir)
		if err != nil {
			t.Fatal(err)
		}
		histL = append(histL, seg...)
		histR = append(histR, seg...)
		refL = append(refL, referenceConvolve(histL, l)[b*block:]...)
		refR = append(refR, referenceConvolve(histR, r)[b*block:]...)
	}
	if !sliceApprox(gotL, refL, 1e-10) {
		t.Fatalf("left convolver drifted from block-wise reference")
	}
	if !sliceApprox(gotR, refR, 1e-10) {
		t.Fatalf("right convolver drifted from block-wise reference")
	}

	// No NaN/Inf leakage.
	for _, v := range append(append([]float64{}, gotL...), gotR...) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatal("non-finite convolver output")
		}
	}
}

func TestSameSamplingInstantAndUpdate(t *testing.T) {
	mesh, err := makeOctaMesh(nonTrivialFilters())
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)
	lc := eng.NewConvolver(Left)
	rc := eng.NewConvolver(Right)

	dir := Vec3{0.8, -0.2, 0.5}.normalize()
	if err := eng.Update(dir, lc, rc); err != nil {
		t.Fatal(err)
	}

	// Both ears must carry the identical triangle location and weights, i.e.
	// the same sampling instant and geometry decision.
	if lc.Location().Face.ID != rc.Location().Face.ID {
		t.Fatalf("ear face IDs differ: %d vs %d", lc.Location().Face.ID, rc.Location().Face.ID)
	}
	if !sliceApprox(lc.Location().Weights(), rc.Location().Weights(), 1e-15) {
		t.Fatal("ear barycentric weights differ")
	}

	l, r, err := eng.Interpolate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !approx(lc.Delay(), l.Delay, 1e-12) || !approx(rc.Delay(), r.Delay, 1e-12) {
		t.Fatal("installed delays do not match interpolation")
	}

	// Feeding identical input to both ears produces distinct but finite
	// outputs whose timing difference equals the interpolated ITD.
	impulse := make([]float64, 40)
	impulse[0] = 1
	outL := lc.ProcessBlock(impulse)
	outR := rc.ProcessBlock(impulse)
	measuredITD := peakDelay(outL) - peakDelay(outR)
	if !approx(measuredITD, l.Delay-r.Delay, 1e-10) {
		t.Fatalf("streaming ITD %v != interpolated %v", measuredITD, l.Delay-r.Delay)
	}
}

func TestUpdateRejectsBadEarOrder(t *testing.T) {
	mesh, err := makeOctaMesh(nonTrivialFilters())
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(mesh)
	lc := eng.NewConvolver(Left)
	rc := eng.NewConvolver(Right)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on swapped ear convolvers")
		}
	}()
	_ = eng.Update(Vec3{1, 1, 1}, rc, lc)
}
