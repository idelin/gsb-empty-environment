package animblend

import (
	"math"
	"testing"
)

func TestBlendLayeredMask(t *testing.T) {
	base := NewLocalPose(3)
	layer := NewLocalPose(3)
	layer.Rotations[0] = rz(math.Pi / 2)
	layer.Rotations[1] = rz(math.Pi / 2)
	layer.Translations[1] = Vec3{Y: 4}

	// Only bone 1 belongs to the upper-body mask.
	mask := BoneMask{0, 1}
	out := BlendLayered(base, layer, mask)

	quatsSameRotation(t, "bone0 keeps base", out.Rotations[0], IdentityQuat())
	quatsSameRotation(t, "bone1 takes layer", out.Rotations[1], rz(math.Pi/2))
	approxEqVec(t, "bone0 translation untouched", out.Translations[0], Vec3{})
	approxEqVec(t, "bone1 translation from layer", out.Translations[1], Vec3{Y: 4})
	// Bone 2 is absent from the mask: weight 0, base pose retained.
	quatsSameRotation(t, "bone2 defaults to weight 0", out.Rotations[2], IdentityQuat())

	half := BlendLayered(base, layer, BoneMask{0.5, 0})
	quatsSameRotation(t, "half mask midpoint", half.Rotations[0], rz(math.Pi/4))
}

func TestAdditiveRelativeToReference(t *testing.T) {
	reference := NewLocalPose(1)
	reference.Rotations[0] = rz(math.Pi / 2)
	reference.Translations[0] = Vec3{Y: 2}

	clip := NewLocalPose(1)
	clip.Rotations[0] = rz(math.Pi) // extra +90 degrees over reference
	clip.Translations[0] = Vec3{Y: 5, X: 1}

	add := MakeAdditive(clip, reference)
	quatsSameRotation(t, "rotation delta", add.RotationDeltas[0], rz(math.Pi/2))
	approxEqVec(t, "translation delta", add.TranslationDeltas[0], Vec3{X: 1, Y: 3})

	// Full strength on a base equal to the reference reproduces the clip pose.
	full := ApplyAdditive(reference, add, 1)
	quatsSameRotation(t, "full additive reproduces clip", full.Rotations[0], clip.Rotations[0])
	approxEqVec(t, "full additive translation", full.Translations[0], clip.Translations[0])

	// Zero strength leaves the base untouched.
	none := ApplyAdditive(reference, add, 0)
	quatsSameRotation(t, "zero additive keeps base", none.Rotations[0], reference.Rotations[0])
	approxEqVec(t, "zero additive translation", none.Translations[0], reference.Translations[0])

	// Half strength applies half the reference-relative deviation.
	half := ApplyAdditive(reference, add, 0.5)
	quatsSameRotation(t, "half additive rotation", half.Rotations[0], rz(3*math.Pi/4))
	approxEqVec(t, "half additive translation", half.Translations[0], Vec3{X: 0.5, Y: 3.5})
}

func TestBlendLoopingPhaseSynchronized(t *testing.T) {
	// Two clips with different durations share the same two endpoint poses;
	// keyframes are laid out so pose i is identity at phase 0 and rz(pi/2)
	// at the last frame.
	makeClip := func(duration float64) Clip {
		p0 := NewLocalPose(1)
		p1 := NewLocalPose(1)
		p1.Rotations[0] = rz(math.Pi / 2)
		return Clip{Duration: duration, Loop: true, Poses: []LocalPose{p0, p1}}
	}

	clipA := makeClip(1.0)
	clipB := makeClip(3.0)

	// Same phase, different absolute durations: both clips must sample the
	// same point in their cycle, so a 50/50 blend is the midpoint rotation.
	out := BlendLooping(clipA, clipB, 0.5, 0.5)
	quatsSameRotation(t, "phase-synced midpoint", out.Rotations[0], rz(math.Pi/2))
	// At phase 0.25 each clip sits halfway between its two keyframes; a 50/50
	// blend of identical midpoints stays at that midpoint, proving the clips
	// stay in phase despite durations 1.0 vs 3.0.
	inPhase := BlendLooping(clipA, clipB, 0.25, 0.5)
	quatsSameRotation(t, "in-phase quarter blend", inPhase.Rotations[0], rz(math.Pi/4))

	// Endpoints of the blend pick the respective clips.
	onlyA := BlendLooping(clipA, clipB, 0.25, 0)
	onlyB := BlendLooping(clipA, clipB, 0.25, 1)
	quatsSameRotation(t, "weight 0 uses A", onlyA.Rotations[0], rz(math.Pi/4))
	quatsSameRotation(t, "weight 1 uses B", onlyB.Rotations[0], rz(math.Pi/4))

	// Phases wrap onto [0,1): phase 1.25 behaves like 0.25.
	wrapped := BlendLooping(clipA, clipB, 1.25, 0.5)
	quatsSameRotation(t, "wrapped phase", wrapped.Rotations[0], rz(math.Pi/4))
}

func TestSampleClipWrappingAndClamp(t *testing.T) {
	p0 := NewLocalPose(1)
	p1 := NewLocalPose(1)
	p1.Rotations[0] = rz(math.Pi / 2)

	looping := Clip{Duration: 1, Loop: true, Poses: []LocalPose{p0, p1}}
	quatsSameRotation(t, "loop wraps past duration", SampleClip(looping, 1.0).Rotations[0], p0.Rotations[0])
	quatsSameRotation(t, "loop negative time", SampleClip(looping, -0.5).Rotations[0], p1.Rotations[0])

	once := Clip{Duration: 1, Loop: false, Poses: []LocalPose{p0, p1}}
	quatsSameRotation(t, "non-loop clamps end", SampleClip(once, 5).Rotations[0], p1.Rotations[0])
	quatsSameRotation(t, "non-loop clamps start", SampleClip(once, -5).Rotations[0], p0.Rotations[0])
}

func TestRunAndGunIntegration(t *testing.T) {
	// root, spine, arm (masked upper body), leg (lower body).
	skel := Skeleton{Bones: []Bone{
		{Name: "root", Parent: -1},
		{Name: "spine", Parent: 0},
		{Name: "arm", Parent: 1},
		{Name: "leg", Parent: 0},
	}}

	run := NewLocalPose(4)
	run.Rotations[2] = IdentityQuat() // neutral arm so the aim layer is exact
	run.Rotations[3] = rx(0.8)        // running leg
	run.Translations[3] = Vec3{Z: 0.5}

	aim := NewLocalPose(4)
	aim.Rotations[2] = rz(math.Pi / 2) // aim arm forward

	reference := NewLocalPose(4)
	additive := MakeAdditive(aim, reference)

	upperBody := BoneMask{0, 1, 1, 0}
	combined := BlendLayered(run, ApplyAdditive(run, additive, 1), upperBody)

	// Arm follows the aim pose; the running leg is preserved by the mask.
	quatsSameRotation(t, "aim arm", combined.Rotations[2], aim.Rotations[2])
	quatsSameRotation(t, "run leg preserved", combined.Rotations[3], run.Rotations[3])
	approxEqVec(t, "run leg translation preserved", combined.Translations[3], Vec3{Z: 0.5})

	world := WorldMatrices(skel, combined)
	if len(world) != 4 {
		t.Fatalf("got %d world matrices, want 4", len(world))
	}
	// Parent order guarantees the arm world matrix already includes the spine.
	if got := world[2].at(3, 3); math.Abs(got-1) > eps {
		t.Errorf("arm world matrix not homogeneous: w=%v", got)
	}
}
