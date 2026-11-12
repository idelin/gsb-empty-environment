package taskpkg

import (
	"math"
	"testing"
)

func TestQuatFromToAndRotate(t *testing.T) {
	ref := Vec3{Y: 1}
	cases := []struct {
		name string
		u, v Vec3
	}{
		{"aligned", Vec3{X: 1}, Vec3{X: 1}},
		{"quadrant", Vec3{X: 1}, Vec3{Z: 1}},
		{"obtuse", Vec3{X: 1}, Vec3{X: -0.6, Y: 0.8}},
		{"anti-parallel", Vec3{X: 1}, Vec3{X: -1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QuatFromTo(c.u, c.v, ref)
			got := RotateVector(q, c.u)
			vecApprox(t, got, Normalize(c.v), 1e-9, "rotated vector")
		})
	}
}

func TestQuatInverseAndMul(t *testing.T) {
	q := QuatFromAxisAngle(Vec3{Z: 1}, 1.234)
	product := QuatMul(q, QuatInverse(q))
	if got := QuatAngle(product, IdentityQuat()); got > 1e-12 {
		t.Fatalf("q * q^-1 angle from identity: %.3e", got)
	}
	v := Vec3{X: 0.3, Y: -0.7, Z: 0.2}
	back := Rotate(QuatInverse(q), Rotate(q, v))
	vecApprox(t, back, v, 1e-12, "round-trip rotation")
}

func TestPerpToDegeneracy(t *testing.T) {
	ref := Vec3{Y: 1}
	for _, dir := range []Vec3{{Y: 1}, {Y: -1}} {
		p := PerpTo(dir, ref)
		if Length(p) < 1-Epsilon {
			t.Fatalf("degenerate perpendicular not unit: %+v", p)
		}
		if Dot(p, dir) > Epsilon {
			t.Fatalf("perpendicular not orthogonal to %+v", dir)
		}
	}
}

// TestTwoBoneMatchesChain cross-validates the analytic two-bone solution
// against the iterative chain solver on a reachable, unconstrained target.
func TestTwoBoneMatchesChain(t *testing.T) {
	root := Vec3{X: 0.2, Y: 1, Z: -0.4}
	target := Add(root, Vec3{X: 1.1, Y: 0.5, Z: -0.7})
	hint := Vec3{Z: 1}
	analytic, err := SolveTwoBone(TwoBoneRequest{
		UpperLength:   1,
		LowerLength:   1,
		Root:          root,
		Target:        target,
		BendHint:      hint,
		ReferenceAxis: Vec3{Y: 1},
	})
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	restDir := Normalize(Sub(target, root))
	// Start from a legal rest pose pre-bent toward the hint side so the
	// iterative solver selects the same branch as the analytic solution.
	hintDir := PerpTo(restDir, Vec3{Y: 1})
	elbowRest := Add(root, Add(Scale(restDir, 0.8), Scale(hintDir, 0.6)))
	rest := []Vec3{
		root,
		elbowRest,
		Add(elbowRest, Normalize(Sub(target, elbowRest))),
	}
	rest[1] = Add(root, Scale(Normalize(Sub(rest[1], rest[0])), 1.0))
	rest[2] = Add(rest[1], Scale(Normalize(Sub(rest[2], rest[1])), 1.0))
	chain, err := SolveChain(ChainRequest{
		Lengths:       []float64{1, 1},
		Root:          root,
		Target:        target,
		RestPositions: rest,
		Limits: []JointLimit{
			{HalfAngle: math.Pi, PoleHint: hint},
			{HalfAngle: math.Pi, PoleHint: hint},
		},
		MaxIterations: 128,
		Tolerance:     1e-10,
		ReferenceAxis: Vec3{Y: 1},
	})
	if err != nil {
		t.Fatalf("SolveChain: %v", err)
	}
	if !chain.Converged {
		t.Fatalf("chain did not converge: residual %.3e", chain.Residual)
	}
	vecApprox(t, chain.Positions[1], analytic.Elbow, 1e-4, "elbow")
	vecApprox(t, chain.Positions[2], analytic.Tip, 1e-6, "tip")
}

func TestTwoBoneTooCloseFold(t *testing.T) {
	req := TwoBoneRequest{
		UpperLength:   2.0,
		LowerLength:   1.0,
		Root:          Vec3{},
		Target:        Vec3{X: 0.5},
		BendHint:      Vec3{Z: 1},
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveTwoBone(req)
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	if res.Reachable {
		t.Fatal("target inside minimum reach must be reported unreachable")
	}
	if got := Distance(req.Root, res.Elbow); !approx(got, 2.0, 1e-9) {
		t.Fatalf("upper length %.9f", got)
	}
	if got := Distance(res.Elbow, res.Tip); !approx(got, 1.0, 1e-9) {
		t.Fatalf("lower length %.9f", got)
	}
	// The closest feasible tip sits at the minimum reach (1.0) toward target.
	vecApprox(t, res.Tip, Vec3{X: 1}, 1e-9, "minimum-reach tip")
	if !approx(res.Residual, 0.5, 1e-9) {
		t.Fatalf("residual %.9f, want 0.5", res.Residual)
	}

	// A reachable, non-degenerate target still honors the bend hint.
	bent, err := SolveTwoBone(TwoBoneRequest{
		UpperLength:   2.0,
		LowerLength:   1.0,
		Root:          Vec3{},
		Target:        Vec3{X: 1.5},
		BendHint:      Vec3{Z: 1},
		ReferenceAxis: Vec3{Y: 1},
	})
	if err != nil {
		t.Fatalf("SolveTwoBone bent: %v", err)
	}
	if bent.Elbow.Z <= 0 {
		t.Fatalf("bend should follow the hinted +Z side, elbow %+v", bent.Elbow)
	}
}

func TestTwoBoneTargetAtRoot(t *testing.T) {
	req := TwoBoneRequest{
		UpperLength:   2.0,
		LowerLength:   1.0,
		Root:          Vec3{},
		Target:        Vec3{},
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveTwoBone(req)
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	if res.Reachable {
		t.Fatal("coincident root/target with unequal bones is unreachable")
	}
	for _, p := range []Vec3{res.Elbow, res.Tip} {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsNaN(p.Z) {
			t.Fatalf("NaN in degenerate solution: %+v", p)
		}
	}
	if got := Distance(req.Root, res.Elbow); !approx(got, 2.0, 1e-9) {
		t.Fatalf("upper length %.9f", got)
	}
	if got := Distance(res.Elbow, res.Tip); !approx(got, 1.0, 1e-9) {
		t.Fatalf("lower length %.9f", got)
	}
	if got := Distance(res.Tip, req.Target); !approx(got, 1.0, 1e-9) {
		t.Fatalf("minimum-reach tip distance %.9f, want 1", got)
	}
}

func TestTwoBoneValidation(t *testing.T) {
	if _, err := SolveTwoBone(TwoBoneRequest{UpperLength: 0, LowerLength: 1}); err == nil {
		t.Fatal("expected error for zero upper length")
	}
	if _, err := SolveTwoBone(TwoBoneRequest{UpperLength: 1, LowerLength: -2}); err == nil {
		t.Fatal("expected error for negative lower length")
	}
}

func TestTwoBoneEqualBonesFoldAtRoot(t *testing.T) {
	req := TwoBoneRequest{
		UpperLength:   1.5,
		LowerLength:   1.5,
		Root:          Vec3{},
		Target:        Vec3{},
		BendHint:      Vec3{Z: 1},
		ReferenceAxis: Vec3{Y: 1},
	}
	res, err := SolveTwoBone(req)
	if err != nil {
		t.Fatalf("SolveTwoBone: %v", err)
	}
	if res.Reachable {
		t.Fatal("coincident root/target is unreachable")
	}
	if got := Distance(req.Root, res.Elbow); !approx(got, 1.5, 1e-9) {
		t.Fatalf("upper length %.9f", got)
	}
	if got := Distance(res.Elbow, res.Tip); !approx(got, 1.5, 1e-9) {
		t.Fatalf("lower length %.9f", got)
	}
	vecApprox(t, res.Tip, req.Root, 1e-9, "fully folded tip")
}

func TestMathHelpers(t *testing.T) {
	if got := Lerp(Vec3{X: 0}, Vec3{X: 10}, 0.25); !approx(got.X, 2.5, 1e-12) {
		t.Fatalf("Lerp = %+v", got)
	}
	if got := LengthSq(Vec3{X: 3, Z: 4}); !approx(got, 25, 1e-12) {
		t.Fatalf("LengthSq = %v", got)
	}
	if q := QuatFromAxisAngle(Vec3{}, 1.2); QuatAngle(q, IdentityQuat()) > 1e-12 {
		t.Fatalf("zero-axis quaternion should be identity, got %+v", q)
	}
	if q := QuatFromAxisAngle(Vec3{Y: 1}, 0); QuatAngle(q, IdentityQuat()) > 1e-12 {
		t.Fatalf("zero-angle quaternion should be identity, got %+v", q)
	}
	if got := Normalize(Vec3{}); Length(got) > Epsilon {
		t.Fatalf("degenerate normalize produced %+v", got)
	}
	if !approx(Angle(Vec3{X: 1}, Vec3{Y: 1}), math.Pi/2, 1e-12) {
		t.Fatal("Angle of perpendicular vectors should be pi/2")
	}
}
