package taskpkg

import (
	"math"
	"testing"
)

const testTol = 1e-6

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func vecApprox(t *testing.T, got, want Vec3, tol float64, label string) {
	t.Helper()
	if Distance(got, want) > tol {
		t.Fatalf("%s = %+v, want %+v (dist %.3e)", label, got, want, Distance(got, want))
	}
}

// reconstructFK rebuilds joint positions from the returned local rotations,
// matching the frame convention documented on ChainResult.
func reconstructFK(t *testing.T, res ChainResult, req ChainRequest) []Vec3 {
	t.Helper()
	n := len(req.Lengths)
	restDirs, err := restDirections(req, n)
	if err != nil {
		t.Fatalf("rest directions: %v", err)
	}
	worldQ := make([]Quat, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			worldQ[i] = res.Rotations[0]
		} else {
			worldQ[i] = QuatMul(worldQ[i-1], res.Rotations[i])
		}
	}
	p := make([]Vec3, n+1)
	p[0] = req.Root
	for i := 0; i < n; i++ {
		dir := RotateVector(worldQ[i], restDirs[i])
		p[i+1] = Add(p[i], Scale(dir, req.Lengths[i]))
	}
	return p
}

func assertBoneLengths(t *testing.T, req ChainRequest, p []Vec3) {
	t.Helper()
	for i := range req.Lengths {
		if got := Distance(p[i], p[i+1]); !approx(got, req.Lengths[i], 1e-9) {
			t.Fatalf("bone %d length = %.9f, want %.9f", i, got, req.Lengths[i])
		}
	}
}

func assertCones(t *testing.T, req ChainRequest, p []Vec3) {
	t.Helper()
	n := len(req.Lengths)
	restDirs, err := restDirections(req, n)
	if err != nil {
		t.Fatalf("rest directions: %v", err)
	}
	ref := req.ReferenceAxis
	if Length(ref) < Epsilon {
		ref = Vec3{Y: 1}
	}
	for i := 0; i < n; i++ {
		lim := req.Limits[i]
		if lim.HalfAngle >= math.Pi-Epsilon {
			continue
		}
		d := Normalize(Sub(p[i+1], p[i]))
		axis, _ := coneFrame(i, p, restDirs, lim, ref)
		if got := Angle(d, axis); got > lim.HalfAngle+1e-7 {
			t.Fatalf("joint %d cone violated: angle %.6f > half %.6f", i, got, lim.HalfAngle)
		}
	}
}

func TestTwoBoneAnalytic(t *testing.T) {
	req := TwoBoneRequest{
		UpperLength:   1.0,
		LowerLength:   1.0,
		Root:          Vec3{},
		Target:        Vec3{X: 1.2, Y: -0.4, Z: 0},
		BendHint:      Vec3{Z: 1},
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveTwoBone(req)
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	if !res.Reachable {
		t.Fatal("reachable target reported unreachable")
	}
	if got := Distance(req.Root, res.Elbow); !approx(got, 1.0, 1e-9) {
		t.Fatalf("upper length %.9f", got)
	}
	if got := Distance(res.Elbow, res.Tip); !approx(got, 1.0, 1e-9) {
		t.Fatalf("lower length %.9f", got)
	}
	vecApprox(t, res.Tip, req.Target, 1e-9, "tip")
	if res.Residual > 1e-9 {
		t.Fatalf("residual %.3e", res.Residual)
	}
	// Elbow must lie on the hinted (+Z) side of the root->target line.
	if res.Elbow.Z <= 0 {
		t.Fatalf("elbow bent to wrong side: %+v", res.Elbow)
	}
	// Reconstruct the tip from the local quaternions.
	restDir := Normalize(Sub(req.Target, req.Root))
	lowerWorld := QuatMul(res.Rotations[0], res.Rotations[1])
	elbow2 := Add(req.Root, Scale(RotateVector(res.Rotations[0], restDir), req.UpperLength))
	tip2 := Add(elbow2, Scale(RotateVector(lowerWorld, restDir), req.LowerLength))
	vecApprox(t, tip2, req.Target, 1e-8, "quaternion FK tip")
}

func TestTwoBoneUnreachable(t *testing.T) {
	req := TwoBoneRequest{
		UpperLength:   1.0,
		LowerLength:   1.0,
		Root:          Vec3{},
		Target:        Vec3{X: 3},
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveTwoBone(req)
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	if res.Reachable {
		t.Fatal("out-of-reach target reported reachable")
	}
	vecApprox(t, res.Tip, Vec3{X: 2}, 1e-9, "fully extended tip")
	if !approx(res.Residual, 1.0, 1e-9) {
		t.Fatalf("residual %.9f, want 1", res.Residual)
	}
}

func TestChainMultiBoneKnownPose(t *testing.T) {
	// Rest pose: straight chain along +X rooted off the world origin.
	lengths := []float64{1, 1, 1, 1}
	root := Vec3{Y: 2}
	rest := make([]Vec3, 5)
	rest[0] = root
	for i := 1; i <= 4; i++ {
		rest[i] = Add(rest[i-1], Vec3{X: 1})
	}
	req := ChainRequest{
		Lengths:       lengths,
		Root:          root,
		Target:        Vec3{X: 3.2, Y: 0.8, Z: 0.4},
		RestPositions: rest,
		MaxIterations: 64,
		Tolerance:     testTol,
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveChain(req)
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	if !res.Converged {
		t.Fatalf("expected convergence, residual %.3e after %d iters", res.Residual, res.Iterations)
	}
	if !res.Reachable {
		t.Fatal("in-range target reported unreachable")
	}
	assertBoneLengths(t, req, res.Positions)
	vecApprox(t, res.Positions[4], req.Target, testTol, "tip")
	if res.Residual > testTol {
		t.Fatalf("recomputed residual %.3e", res.Residual)
	}
	fk := reconstructFK(t, res, req)
	for i := range fk {
		vecApprox(t, fk[i], res.Positions[i], 1e-8, "FK position")
	}
}

func TestChainConeLimitsAndBoundaryCrossing(t *testing.T) {
	lengths := []float64{1.5, 1.5}
	rest := []Vec3{{}, {X: 1.5}, {X: 3}}
	limits := []JointLimit{
		// Root cone: thigh tilts at most 60 degrees off its rest +X axis.
		{HalfAngle: math.Pi / 3},
		// Knee: at most a 25-degree bend relative to the thigh.
		{HalfAngle: 25 * math.Pi / 180, PoleHint: Vec3{Z: 1}},
	}

	// Sweep the target through directions that cross the root cone boundary.
	for deg := 0.0; deg <= 180.0; deg += 5 {
		theta := deg * math.Pi / 180
		target := Vec3{X: 2.4 * math.Cos(theta), Z: 2.4 * math.Sin(theta)}
		req := ChainRequest{
			Lengths:       lengths,
			Root:          Vec3{},
			Target:        target,
			Limits:        limits,
			RestPositions: rest,
			MaxIterations: 64,
			Tolerance:     1e-8,
			ReferenceAxis: Vec3{Y: 1},
		}
		res, err := SolveChain(req)
		if err != nil {
			t.Fatalf("deg %.0f: %v", deg, err)
		}
		assertBoneLengths(t, req, res.Positions)
		assertCones(t, req, res.Positions)

		rootAxis, _ := coneFrame(0, res.Positions, []Vec3{{X: 1}, {X: 1}}, limits[0], Vec3{Y: 1})
		rootAngle := Angle(Normalize(Sub(res.Positions[1], res.Positions[0])), rootAxis)
		if rootAngle > math.Pi/3+1e-7 {
			t.Fatalf("deg %.0f: root cone broken (%.6f)", deg, rootAngle)
		}
		if res.Converged {
			vecApprox(t, res.Positions[2], target, 1e-7, "converged tip")
		} else if res.Residual <= 1e-8 {
			t.Fatalf("deg %.0f: non-converged with zero residual", deg)
		}
	}
}

func TestKneeDoesNotHyperextend(t *testing.T) {
	// Vertical leg with rest pointing down (-Y). The foot stays on the
	// vertical line; the cone + pole hint force a stable forward (+Z) bend
	// instead of any backward knee flip.
	lengths := []float64{1, 1}
	rest := []Vec3{{}, {Y: -1}, {Y: -2}}
	limits := []JointLimit{
		{HalfAngle: math.Pi},
		{HalfAngle: 20 * math.Pi / 180, PoleHint: Vec3{Z: 1}},
	}
	req := ChainRequest{
		Lengths:       lengths,
		Root:          Vec3{},
		Target:        Vec3{Y: -1.7},
		Limits:        limits,
		RestPositions: rest,
		MaxIterations: 64,
		Tolerance:     1e-9,
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveChain(req)
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	assertBoneLengths(t, req, res.Positions)
	assertCones(t, req, res.Positions)
	thigh := Normalize(Sub(res.Positions[1], res.Positions[0]))
	shin := Normalize(Sub(res.Positions[2], res.Positions[1]))
	if Dot(thigh, shin) < math.Cos(20*math.Pi/180)-1e-7 {
		t.Fatalf("knee fold exceeds limit: dot %.6f", Dot(thigh, shin))
	}
	if res.Positions[1].Z < -1e-8 {
		t.Fatalf("knee bent to the negative-Z side: %+v", res.Positions[1])
	}
}

func TestChainUnreachableExtensionCandidate(t *testing.T) {
	lengths := []float64{1, 1, 1}
	rest := []Vec3{{}, {X: 1}, {X: 2}, {X: 3}}
	req := ChainRequest{
		Lengths:       lengths,
		Root:          Vec3{},
		Target:        Vec3{X: 10},
		RestPositions: rest,
		MaxIterations: 16,
		Tolerance:     1e-9,
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveChain(req)
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	if res.Reachable {
		t.Fatal("unreachable target reported reachable")
	}
	assertBoneLengths(t, req, res.Positions)
	vecApprox(t, res.Positions[3], Vec3{X: 3}, 1e-8, "extension tip")
	if !approx(res.Residual, 7.0, 1e-8) {
		t.Fatalf("residual %.9f, want 7", res.Residual)
	}
	// The minimum residual (7.0) exceeds tolerance, so the solver must
	// honestly report non-convergence while still returning the best
	// full-extension candidate.
	if res.Converged {
		t.Fatal("unreachable target with residual above tolerance must not report convergence")
	}
	fk := reconstructFK(t, res, req)
	vecApprox(t, fk[3], res.Positions[3], 1e-8, "FK tip")
}

func TestChainBudgetExhaustedReportsUnconverged(t *testing.T) {
	// A tight root cone blocks the target; one iteration cannot satisfy it.
	lengths := []float64{1, 1}
	rest := []Vec3{{}, {X: 1}, {X: 2}}
	limits := []JointLimit{
		{HalfAngle: 5 * math.Pi / 180},
		{HalfAngle: math.Pi},
	}
	req := ChainRequest{
		Lengths:       lengths,
		Root:          Vec3{},
		Target:        Vec3{X: 1, Y: 1.7},
		Limits:        limits,
		RestPositions: rest,
		MaxIterations: 1,
		Tolerance:     1e-9,
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveChain(req)
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	if res.Converged {
		t.Fatal("single-iteration constrained solve must not report convergence")
	}
	if res.Iterations != 1 {
		t.Fatalf("iterations = %d, want 1", res.Iterations)
	}
	if res.Residual <= 1e-9 {
		t.Fatalf("residual should remain nonzero, got %.3e", res.Residual)
	}
	assertBoneLengths(t, req, res.Positions)
	assertCones(t, req, res.Positions)
}

func TestChainInputValidation(t *testing.T) {
	cases := []ChainRequest{
		{Lengths: nil, Root: Vec3{}, Target: Vec3{X: 1}},
		{Lengths: []float64{1, 0}, Root: Vec3{}, Target: Vec3{X: 1}},
		{Lengths: []float64{1, -1}, Root: Vec3{}, Target: Vec3{X: 1}},
		{Lengths: []float64{1, 1}, Root: Vec3{}, Target: Vec3{X: 1}, Limits: []JointLimit{{HalfAngle: 1}}},
		{Lengths: []float64{1, 1}, Root: Vec3{}, Target: Vec3{X: 1}, RestPositions: []Vec3{{}, {X: 1}}},
		{Lengths: []float64{1, 1}, Root: Vec3{}, Target: Vec3{X: 1}, Limits: []JointLimit{{HalfAngle: -1}, {HalfAngle: 1}}},
	}
	for i, req := range cases {
		if _, err := SolveChain(req); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestPoleDegeneracyUsesReferenceAxis(t *testing.T) {
	// Rest and target are collinear and opposite at the knee; without a
	// reference-based fallback the pole cross product would be undefined.
	lengths := []float64{1, 1}
	rest := []Vec3{{}, {Y: -1}, {Y: -2}}
	limits := []JointLimit{
		{HalfAngle: math.Pi},
		{HalfAngle: math.Pi / 6, PoleHint: Vec3{Z: 1}},
	}
	req := ChainRequest{
		Lengths:       lengths,
		Root:          Vec3{},
		Target:        Vec3{Y: -0.1},
		Limits:        limits,
		RestPositions: rest,
		MaxIterations: 32,
		Tolerance:     1e-9,
		ReferenceAxis: Vec3{X: 0, Y: 1, Z: 0},
	}
	res, err := SolveChain(req)
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	for _, p := range res.Positions {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsNaN(p.Z) {
			t.Fatalf("NaN in solved positions: %+v", res.Positions)
		}
	}
	assertBoneLengths(t, req, res.Positions)
	assertCones(t, req, res.Positions)
}
