package taskpkg

import (
	"errors"
	"math"
)

// TwoBoneRequest is the input of the closed-form two-bone (e.g. thigh/shin)
// solver. Lengths are the upper and lower bone lengths. BendHint selects the
// bending side of the solving plane and may be zero; a zero hint or a
// degenerate plane falls back to ReferenceAxis.
type TwoBoneRequest struct {
	UpperLength   float64
	LowerLength   float64
	Root          Vec3
	Target        Vec3
	BendHint      Vec3
	ReferenceAxis Vec3
}

// TwoBoneResult contains the analytic joint positions, local rotations and
// the recomputed tip residual. Reachable is false when the target lies
// outside [|L1-L2|, L1+L2]; in that case the chain is fully extended toward
// the target to minimize the residual.
type TwoBoneResult struct {
	Elbow     Vec3
	Tip       Vec3
	Rotations [2]Quat
	Residual  float64
	Reachable bool
}

// SolveTwoBone computes the law-of-cosines two-bone IK solution. The rest
// pose is defined as a straight chain pointing from Root to Target.
// Rotations[0] is the root world quaternion and Rotations[1] is the elbow
// quaternion relative to the root frame.
func SolveTwoBone(req TwoBoneRequest) (TwoBoneResult, error) {
	if !(req.UpperLength > 0) || !(req.LowerLength > 0) {
		return TwoBoneResult{}, errors.New("two-bone: lengths must be positive")
	}
	l1 := req.UpperLength
	l2 := req.LowerLength
	ref := req.ReferenceAxis
	if Length(ref) < Epsilon {
		ref = Vec3{Y: 1}
	}

	restDir := Normalize(Sub(req.Target, req.Root))
	if Length(restDir) < Epsilon {
		// Target coincides with the root: use the fixed reference axis as
		// the deterministic rest/pole direction.
		restDir = Normalize(ref)
	}

	dist := Distance(req.Root, req.Target)
	maxReach := l1 + l2
	minReach := math.Abs(l1 - l2)
	reachable := dist <= maxReach+Epsilon && dist >= minReach+Epsilon

	inAxis := restDir
	outAxis := PerpTo(inAxis, ref)

	// Pick the bend side so the elbow moves toward BendHint.
	sign := 1.0
	if Length(req.BendHint) > Epsilon {
		if Dot(Normalize(req.BendHint), outAxis) < 0 {
			sign = -1
		}
	}

	var elbow, tip Vec3
	switch {
	case dist > maxReach+Epsilon:
		// Unreachable, too far: straight extension toward the target.
		elbow = Add(req.Root, Scale(restDir, l1))
		tip = Add(elbow, Scale(restDir, l2))
	case dist < minReach+Epsilon:
		// Unreachable, too close (including target == root): clamp the
		// working distance to the minimum feasible |l1-l2| and solve the
		// same law-of-cosines triangle, which keeps both bone lengths exact.
		d := minReach
		if d < Epsilon {
			// Equal bones: fold fully back so the tip coincides with the root.
			elbow = Add(req.Root, Scale(outAxis, sign*l1))
			tip = req.Root
			break
		}
		cosA := (l1*l1 + d*d - l2*l2) / (2 * l1 * d)
		a := math.Acos(clampScalar(cosA, -1, 1))
		elbowDir := Add(
			Scale(inAxis, math.Cos(a)),
			Scale(outAxis, sign*math.Sin(a)),
		)
		elbow = Add(req.Root, Scale(elbowDir, l1))
		tip = Add(req.Root, Scale(restDir, d))
	default:
		// Reachable: law of cosines for the interior root angle.
		cosA := (l1*l1 + dist*dist - l2*l2) / (2 * l1 * dist)
		a := math.Acos(clampScalar(cosA, -1, 1))
		elbowDir := Add(
			Scale(inAxis, math.Cos(a)),
			Scale(outAxis, sign*math.Sin(a)),
		)
		elbow = Add(req.Root, Scale(elbowDir, l1))
		tip = req.Target
	}

	rootQ := QuatFromTo(restDir, Normalize(Sub(elbow, req.Root)), ref)
	lowerRestWorld := restDir
	lowerSolved := Normalize(Sub(tip, elbow))
	lowerWorldQ := QuatFromTo(lowerRestWorld, lowerSolved, ref)
	elbowQ := QuatMul(QuatInverse(rootQ), lowerWorldQ)

	return TwoBoneResult{
		Elbow:     elbow,
		Tip:       tip,
		Rotations: [2]Quat{rootQ, elbowQ},
		Residual:  Distance(tip, req.Target),
		Reachable: reachable,
	}, nil
}
