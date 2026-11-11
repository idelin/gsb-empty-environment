package animblend

// AdditivePose is the difference of a clip pose relative to a reference
// pose. Rotations store deltaRotation = clip * inverse(reference), while
// translations store the plain difference, so applying it to any base pose
// reproduces the clip's deviation from reference.
type AdditivePose struct {
	RotationDeltas    []Quat
	TranslationDeltas []Vec3
}

// MakeAdditive computes the additive transform of pose relative to reference.
func MakeAdditive(pose, reference LocalPose) AdditivePose {
	n := len(pose.Rotations)
	out := AdditivePose{
		RotationDeltas:    make([]Quat, n),
		TranslationDeltas: make([]Vec3, n),
	}
	for i := 0; i < n; i++ {
		out.RotationDeltas[i] = Normalize(Mul(pose.Rotations[i], Inverse(reference.Rotations[i])))
		out.TranslationDeltas[i] = pose.Translations[i].sub(reference.Translations[i])
	}
	return out
}

// ApplyAdditive layers an additive pose onto base with strength alpha. The
// rotation delta is slerped in from identity (partial additivity), and the
// translation delta is scaled by alpha.
func ApplyAdditive(base LocalPose, add AdditivePose, alpha float64) LocalPose {
	alpha = clamp(alpha, 0, 1)
	n := len(base.Rotations)
	out := NewLocalPose(n)
	for i := 0; i < n; i++ {
		partial := Slerp(IdentityQuat(), add.RotationDeltas[i], alpha)
		out.Rotations[i] = Normalize(Mul(partial, base.Rotations[i]))
		out.Translations[i] = base.Translations[i].add(add.TranslationDeltas[i].scale(alpha))
		out.Scales[i] = base.Scales[i]
	}
	return out
}
