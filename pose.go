package animblend

// Bone describes one node of a skeleton. Parent must either be -1 for a root
// or refer to an earlier bone, so parents are always processed before
// children.
type Bone struct {
	Name   string
	Parent int
}

// Skeleton is an ordered list of bones with parents before children.
type Skeleton struct {
	Bones []Bone
}

// Validate reports whether the parent indices form a valid, topologically
// ordered forest.
func (s Skeleton) Validate() bool {
	for i, bone := range s.Bones {
		if bone.Parent < -1 || bone.Parent >= i {
			return false
		}
	}
	return true
}

// LocalPose holds the local-space transform of every bone, indexed by bone.
type LocalPose struct {
	Rotations    []Quat
	Translations []Vec3
	Scales       []Vec3
}

// NewLocalPose allocates a pose for n bones in bind state
// (identity rotation/scale, zero translation).
func NewLocalPose(n int) LocalPose {
	pose := LocalPose{
		Rotations:    make([]Quat, n),
		Translations: make([]Vec3, n),
		Scales:       make([]Vec3, n),
	}
	for i := 0; i < n; i++ {
		pose.Rotations[i] = IdentityQuat()
		pose.Scales[i] = Vec3{X: 1, Y: 1, Z: 1}
	}
	return pose
}

// BoneMask assigns each bone a blend weight in [0, 1]. Bones missing from a
// shorter mask are treated as weight 0.
type BoneMask []float64

// weightAt returns the mask weight for bone i, defaulting to 0.
func (m BoneMask) weightAt(i int) float64 {
	if i < 0 || i >= len(m) {
		return 0
	}
	w := m[i]
	return clamp(w, 0, 1)
}

// BlendPoses performs a whole-pose blend: out = Lerp(base, layer, weight).
// Rotations use shortest-path slerp and are normalized; translations and
// scales are linearly interpolated.
func BlendPoses(base, layer LocalPose, weight float64) LocalPose {
	weight = clamp(weight, 0, 1)
	n := len(base.Rotations)
	out := NewLocalPose(n)
	for i := 0; i < n; i++ {
		out.Rotations[i] = Slerp(base.Rotations[i], layer.Rotations[i], weight)
		out.Translations[i] = lerpVec(base.Translations[i], layer.Translations[i], weight)
		out.Scales[i] = lerpVec(base.Scales[i], layer.Scales[i], weight)
	}
	return out
}

// BlendLayered overlays layer onto base using a per-bone mask. A bone with
// mask weight 0 keeps the base pose entirely; weight 1 takes the layer pose
// entirely. Rotations follow the shortest arc and are normalized.
func BlendLayered(base, layer LocalPose, mask BoneMask) LocalPose {
	n := len(base.Rotations)
	out := NewLocalPose(n)
	for i := 0; i < n; i++ {
		w := mask.weightAt(i)
		out.Rotations[i] = Slerp(base.Rotations[i], layer.Rotations[i], w)
		out.Translations[i] = lerpVec(base.Translations[i], layer.Translations[i], w)
		out.Scales[i] = lerpVec(base.Scales[i], layer.Scales[i], w)
	}
	return out
}

// LocalMatrices converts a local pose into per-bone local transform matrices.
func LocalMatrices(pose LocalPose) []Mat4 {
	n := len(pose.Rotations)
	out := make([]Mat4, n)
	for i := 0; i < n; i++ {
		out[i] = ComposeTRS(pose.Translations[i], pose.Rotations[i], pose.Scales[i])
	}
	return out
}

// WorldMatrices walks the skeleton parent-before-child and accumulates world
// transforms: world[i] = world[parent] * local[i].
func WorldMatrices(skel Skeleton, pose LocalPose) []Mat4 {
	if !skel.Validate() {
		panic("animblend: skeleton has invalid or out-of-order parent indices")
	}
	if len(skel.Bones) != len(pose.Rotations) {
		panic("animblend: pose does not match skeleton bone count")
	}

	local := LocalMatrices(pose)
	world := make([]Mat4, len(local))
	for i, bone := range skel.Bones {
		if bone.Parent < 0 {
			world[i] = local[i]
		} else {
			world[i] = Mul4(world[bone.Parent], local[i])
		}
	}
	return world
}

// WorldPosition returns the world-space origin of a bone from its world matrix.
func WorldPosition(world Mat4) Vec3 {
	return Vec3{X: world[12], Y: world[13], Z: world[14]}
}

func lerpVec(a, b Vec3, t float64) Vec3 {
	return Vec3{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
		Z: a.Z + (b.Z-a.Z)*t,
	}
}
