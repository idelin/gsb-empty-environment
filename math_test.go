package animblend

import (
	"math"
	"testing"
)

const eps = 1e-10

func approxEq(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s = %.12f, want %.12f", name, got, want)
	}
}

func approxEqVec(t *testing.T, name string, got, want Vec3) {
	t.Helper()
	approxEq(t, name+".X", got.X, want.X)
	approxEq(t, name+".Y", got.Y, want.Y)
	approxEq(t, name+".Z", got.Z, want.Z)
}

// quatsSameRotation treats q and -q as the same rotation.
func quatsSameRotation(t *testing.T, name string, got, want Quat) {
	t.Helper()
	d := math.Abs(QuatDot(Normalize(got), Normalize(want)))
	if math.Abs(d-1) > eps {
		t.Errorf("%s = %+v, want same rotation as %+v (|dot|=%.12f)", name, got, want, d)
	}
}

func rz(a float64) Quat { return QuatFromAxisAngle(0, 0, 1, a) }
func rx(a float64) Quat { return QuatFromAxisAngle(1, 0, 0, a) }

func TestSlerpEndpointsAndMidpoint(t *testing.T) {
	a := IdentityQuat()
	b := rz(math.Pi / 2)

	quatsSameRotation(t, "t=0", Slerp(a, b, 0), a)
	quatsSameRotation(t, "t=1", Slerp(a, b, 1), b)

	mid := Slerp(a, b, 0.5)
	quatsSameRotation(t, "midpoint", mid, rz(math.Pi/4))
	if n := mid.Norm(); math.Abs(n-1) > eps {
		t.Errorf("midpoint not unit: norm=%v", n)
	}

	// Rotation direction must match the shortest positive arc.
	rotated := Rotate(mid, Vec3{X: 1})
	inv := 1 / math.Sqrt2
	approxEqVec(t, "rotated x-axis", rotated, Vec3{X: inv, Y: inv})
}

func TestSlerpAntipodalDoesNotFlip(t *testing.T) {
	// Exact antipodes: dot = -1, ordinary slerp divides by sin(pi).
	a := rz(math.Pi / 4)
	b := Negate(a)

	for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
		q := Slerp(a, b, f)
		if math.IsNaN(q.X) || math.IsNaN(q.Y) || math.IsNaN(q.Z) || math.IsNaN(q.W) {
			t.Fatalf("NaN at f=%v: %+v", f, q)
		}
		if n := q.Norm(); math.Abs(n-1) > eps {
			t.Errorf("non-unit at f=%v: %v", f, n)
		}
		// Interior samples stay on the starting hemisphere; the t=1 endpoint
		// legitimately equals b == -a but represents the same rotation.
		if f > 0 && f < 1 && QuatDot(q, a) < -eps {
			t.Errorf("flipped hemisphere at f=%v: dot=%v", f, QuatDot(q, a))
		}
	}

	// Endpoints describe the same rotation even though b == -a.
	quatsSameRotation(t, "antipodal endpoint", Slerp(a, b, 1), a)

	// Near-antipodal inputs take the short way: the midpoint stays within a
	// right angle of the start rather than spinning the long arc.
	near := rz(math.Pi - 1e-9)
	mid := Slerp(IdentityQuat(), near, 0.5)
	angle := 2 * math.Acos(math.Abs(mid.W))
	if angle > math.Pi/2+1e-6 {
		t.Errorf("near-antipodal midpoint took long path: angle=%v", angle)
	}
}

func TestNormalizeQuat(t *testing.T) {
	q := Quat{X: 0, Y: 0, Z: 0.6, W: 0.8}
	scaled := Quat{X: q.X * 3, Y: q.Y * 3, Z: q.Z * 3, W: q.W * 3}
	quatsSameRotation(t, "scaled normalizes", Normalize(scaled), q)
	quatsSameRotation(t, "zero normalizes to identity", Normalize(Quat{}), IdentityQuat())
}

func TestWorldMatricesKnownChain(t *testing.T) {
	// Root at origin, child offset along +X, grandchild offset along +Z.
	skel := Skeleton{Bones: []Bone{
		{Name: "root", Parent: -1},
		{Name: "spine", Parent: 0},
		{Name: "head", Parent: 1},
	}}
	pose := NewLocalPose(3)
	pose.Translations[1] = Vec3{X: 1}
	pose.Translations[2] = Vec3{Z: 1}
	pose.Rotations[1] = rx(math.Pi / 2) // rotates +Z onto -Y

	world := WorldMatrices(skel, pose)

	approxEqVec(t, "root world pos", WorldPosition(world[0]), Vec3{})
	approxEqVec(t, "spine world pos", WorldPosition(world[1]), Vec3{X: 1})

	// Head: parent rotation maps local +Z translation to world -Y.
	approxEqVec(t, "head world pos", WorldPosition(world[2]), Vec3{X: 1, Y: -1})

	// The head's local +Z axis must point to world -Y through the parent.
	zAxis := TransformPoint(world[2], Vec3{Z: 1}).sub(WorldPosition(world[2]))
	approxEqVec(t, "head z axis", zAxis, Vec3{Y: -1})

	// Invalid parent order must be rejected.
	bad := Skeleton{Bones: []Bone{{Name: "a", Parent: 1}, {Name: "b", Parent: -1}}}
	if bad.Validate() {
		t.Fatal("expected invalid skeleton to fail Validate")
	}
}
