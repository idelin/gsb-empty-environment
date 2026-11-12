package taskpkg

import (
	"errors"
	"math"
)

// Default solver budget and tolerance used when the request leaves them zero.
const (
	DefaultMaxIterations = 32
	DefaultTolerance     = 1e-6
)

// JointLimit describes the local rotation cone of one joint.
//
// The cone constrains the outgoing bone of the joint to stay within
// HalfAngle radians of ConeAxis. Vectors are expressed in the rest-pose
// frame: a zero ConeAxis means the rest direction of that bone, and child
// frames are carried into the current pose by the rotation of the parent
// bone. A HalfAngle >= Pi disables the limit. PoleHint only resolves the
// anti-parallel pole-axis degeneracy; when it is zero the fixed
// ReferenceAxis is used.
type JointLimit struct {
	HalfAngle float64
	ConeAxis  Vec3
	PoleHint  Vec3
}

// ChainRequest is the input of the constrained FABRIK chain solver.
//
// Lengths holds one bone length per joint/bone. Root is the fixed chain
// origin and Target is the desired tip position. Limits has one entry per
// bone (an empty slice means unrestricted joints). RestPositions optionally
// provides n+1 rest-pose joint positions used to build local frames and the
// initial seed; only its directions are used. ReferenceAxis is the fixed
// world axis used when a pole axis degenerates.
type ChainRequest struct {
	Lengths       []float64
	Root          Vec3
	Target        Vec3
	Limits        []JointLimit
	RestPositions []Vec3
	MaxIterations int
	Tolerance     float64
	ReferenceAxis Vec3
}

// ChainResult is the solver output.
//
// Positions are the solved joint positions (len = len(Lengths)+1). Rotations
// are local joint rotations, one per bone/joint: Rotations[0] is the root
// world rotation and Rotations[i] is relative to the parent joint frame.
// Residual is the recomputed tip-to-target distance. Reachable reports
// geometric reachability ignoring cones. Converged is true only when the
// residual reached Tolerance before the iteration budget was spent.
type ChainResult struct {
	Positions  []Vec3
	Rotations  []Quat
	Residual   float64
	Reachable  bool
	Converged  bool
	Iterations int
}

// SolveChain runs a forward/backward position-only FABRIK iteration with
// per-joint cone constraints. Every projection restores bone lengths and
// clips the bone direction into its joint cone. Unreachable targets produce
// the minimum-residual full-extension candidate, and exhausting the
// iteration budget returns the best candidate with Converged == false.
func SolveChain(req ChainRequest) (ChainResult, error) {
	n := len(req.Lengths)
	if n == 0 {
		return ChainResult{}, errors.New("chain: at least one bone length is required")
	}
	totalLength := 0.0
	for i, l := range req.Lengths {
		if !(l > 0) || math.IsNaN(l) || math.IsInf(l, 0) {
			return ChainResult{}, errors.New("chain: bone lengths must be finite and positive")
		}
		if i < len(req.Limits) {
			lim := req.Limits[i]
			if lim.HalfAngle < 0 || math.IsNaN(lim.HalfAngle) {
				return ChainResult{}, errors.New("chain: cone half-angles must be non-negative")
			}
		}
		totalLength += l
	}
	if len(req.Limits) != 0 && len(req.Limits) != n {
		return ChainResult{}, errors.New("chain: Limits must have one entry per bone or be empty")
	}
	if len(req.RestPositions) != 0 && len(req.RestPositions) != n+1 {
		return ChainResult{}, errors.New("chain: RestPositions must provide n+1 joint positions")
	}

	maxIter := req.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}
	tol := req.Tolerance
	if tol <= 0 {
		tol = DefaultTolerance
	}
	ref := req.ReferenceAxis
	if Length(ref) < Epsilon {
		ref = Vec3{Y: 1}
	}
	limits := req.Limits
	if len(limits) == 0 {
		limits = make([]JointLimit, n)
		for i := range limits {
			limits[i] = JointLimit{HalfAngle: math.Pi}
		}
	}

	restDirs, err := restDirections(req, n)
	if err != nil {
		return ChainResult{}, err
	}

	rootTargetDist := Distance(req.Root, req.Target)
	reachable := rootTargetDist <= totalLength+tol

	// Initial seed: supplied rest positions scaled to the requested bone
	// lengths, or a straight chain pointing at the target.
	p := seedPositions(req, restDirs, n)

	best := cloneVecs(p)
	bestRes := Distance(p[n], req.Target)

	// Full-extension candidate along root -> target. It is the optimal
	// answer for unreachable targets and a useful baseline otherwise.
	straight := extensionPositions(req, restDirs, n)
	if straightFeasible(straight, limits, restDirs, ref, n) {
		if r := Distance(straight[n], req.Target); r < bestRes {
			best = cloneVecs(straight)
			bestRes = r
		}
	}

	converged := false
	iterations := 0
	for iterations < maxIter {
		iterations++

		// Backward pass: pin the tip to the target and walk root-ward.
		p[n] = req.Target
		for i := n - 1; i >= 0; i-- {
			d := Normalize(Sub(p[i+1], p[i]))
			d = constrainDir(i, p, d, limits, restDirs, ref)
			p[i] = Add(p[i+1], Scale(Neg(d), req.Lengths[i]))
		}

		// Forward pass: pin the root and walk tip-ward.
		p[0] = req.Root
		for i := 0; i < n; i++ {
			d := Normalize(Sub(p[i+1], p[i]))
			d = constrainDir(i, p, d, limits, restDirs, ref)
			p[i+1] = Add(p[i], Scale(d, req.Lengths[i]))
		}

		residual := Distance(p[n], req.Target)
		if residual < bestRes {
			bestRes = residual
			best = cloneVecs(p)
		}
		if residual <= tol {
			converged = true
			break
		}
	}

	rotations := localRotations(best, restDirs, ref, n)
	return ChainResult{
		Positions:  best,
		Rotations:  rotations,
		Residual:   Distance(best[n], req.Target),
		Reachable:  reachable,
		Converged:  converged,
		Iterations: iterations,
	}, nil
}

// restDirections extracts unit bone directions from the rest pose. When no
// rest positions are supplied the chain rests straight toward the target.
func restDirections(req ChainRequest, n int) ([]Vec3, error) {
	dirs := make([]Vec3, n)
	if len(req.RestPositions) == n+1 {
		for i := 0; i < n; i++ {
			d := Sub(req.RestPositions[i+1], req.RestPositions[i])
			if Length(d) < Epsilon {
				return nil, errors.New("chain: rest pose contains a zero-length bone")
			}
			dirs[i] = Normalize(d)
		}
		return dirs, nil
	}
	dir := Normalize(Sub(req.Target, req.Root))
	if Length(dir) < Epsilon {
		dir = Vec3{X: 1}
	}
	for i := range dirs {
		dirs[i] = dir
	}
	return dirs, nil
}

// seedPositions builds the starting joint layout using rest directions and
// the requested (authoritative) bone lengths.
func seedPositions(req ChainRequest, restDirs []Vec3, n int) []Vec3 {
	p := make([]Vec3, n+1)
	p[0] = req.Root
	for i := 0; i < n; i++ {
		p[i+1] = Add(p[i], Scale(restDirs[i], req.Lengths[i]))
	}
	return p
}

// extensionPositions lays the chain out in a straight line from root toward
// target, which minimizes the residual for any unreachable target.
func extensionPositions(req ChainRequest, restDirs []Vec3, n int) []Vec3 {
	dir := Normalize(Sub(req.Target, req.Root))
	if Length(dir) < Epsilon {
		dir = restDirs[0]
	}
	p := make([]Vec3, n+1)
	p[0] = req.Root
	for i := 0; i < n; i++ {
		p[i+1] = Add(p[i], Scale(dir, req.Lengths[i]))
	}
	return p
}

// straightFeasible reports whether the full-extension layout satisfies every
// joint cone without needing projection.
func straightFeasible(p []Vec3, limits []JointLimit, restDirs []Vec3, ref Vec3, n int) bool {
	for i := 0; i < n; i++ {
		if limits[i].HalfAngle >= math.Pi-Epsilon {
			continue
		}
		d := Normalize(Sub(p[i+1], p[i]))
		axis, _ := coneFrame(i, p, restDirs, limits[i], ref)
		if Angle(d, axis) > limits[i].HalfAngle+1e-9 {
			return false
		}
	}
	return true
}

// coneFrame resolves the world-space cone axis and pole hint for bone i.
// Child joint vectors are carried by the rotation of the parent bone; the
// root frame stays fixed to the rest pose.
func coneFrame(i int, p, restDirs []Vec3, lim JointLimit, ref Vec3) (axis, hint Vec3) {
	if i == 0 {
		axis = lim.ConeAxis
		if Length(axis) < Epsilon {
			axis = restDirs[0]
		}
		hint = lim.PoleHint
		if Length(hint) < Epsilon {
			hint = ref
		}
		return axis, hint
	}
	parentDir := Normalize(Sub(p[i], p[i-1]))
	carry := QuatFromTo(restDirs[i-1], parentDir, ref)
	axis = lim.ConeAxis
	if Length(axis) < Epsilon {
		axis = restDirs[i]
	}
	axis = RotateVector(carry, axis)
	hint = lim.PoleHint
	if Length(hint) < Epsilon {
		hint = ref
	} else {
		hint = RotateVector(carry, hint)
	}
	return axis, hint
}

// constrainDir projects bone direction d into the cone of joint i. The bone
// length itself is restored by the caller, so only the direction changes.
func constrainDir(i int, p []Vec3, d Vec3, limits []JointLimit, restDirs []Vec3, ref Vec3) Vec3 {
	lim := limits[i]
	if lim.HalfAngle >= math.Pi-Epsilon {
		return Normalize(d)
	}
	axis, hint := coneFrame(i, p, restDirs, lim, ref)
	d = Normalize(d)
	ang := Angle(d, axis)
	if ang <= lim.HalfAngle+1e-9 {
		return d
	}
	var rotAxis Vec3
	if ang > math.Pi-Epsilon {
		// Anti-parallel pole degeneracy: pick a deterministic plane with
		// the fixed reference axis.
		rotAxis = PerpTo(axis, hint)
	} else {
		rotAxis = Normalize(Cross(axis, d))
	}
	return RotateVector(QuatFromAxisAngle(rotAxis, lim.HalfAngle), axis)
}

// localRotations converts solved world bone directions into local joint
// quaternions. Rotations[0] is world-space; later entries are relative to
// the parent joint's world rotation.
func localRotations(p, restDirs []Vec3, ref Vec3, n int) []Quat {
	world := make([]Quat, n)
	for i := 0; i < n; i++ {
		solved := Normalize(Sub(p[i+1], p[i]))
		world[i] = QuatFromTo(restDirs[i], solved, ref)
	}
	local := make([]Quat, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			local[i] = world[0]
		} else {
			local[i] = QuatMul(QuatInverse(world[i-1]), world[i])
		}
	}
	return local
}

func cloneVecs(a []Vec3) []Vec3 {
	out := make([]Vec3, len(a))
	copy(out, a)
	return out
}
