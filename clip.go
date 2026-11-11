package animblend

import "math"

// Clip is a sampled animation: evenly spaced keyframe poses covering the
// half-open interval [0, Duration). Poses must all match one skeleton.
type Clip struct {
	Name     string
	Duration float64
	Loop     bool
	Poses    []LocalPose
}

func (c Clip) frameCount() int {
	return len(c.Poses)
}

func (c Clip) frameInterval() float64 {
	if c.frameCount() <= 1 {
		return 0
	}
	return c.Duration / float64(c.frameCount())
}

// SampleClip evaluates the clip at time t. Looping clips wrap t onto
// [0, Duration); non-looping clips clamp to the end pose. Rotations are
// slerped along the shortest arc and normalized.
func SampleClip(c Clip, t float64) LocalPose {
	n := c.frameCount()
	if n == 0 {
		panic("animblend: clip has no poses")
	}
	if n == 1 || c.frameInterval() == 0 {
		return clonePose(c.Poses[0])
	}

	if c.Loop {
		t = wrapPositive(t, c.Duration)
	} else if t <= 0 {
		return clonePose(c.Poses[0])
	} else if t >= c.Duration {
		return clonePose(c.Poses[n-1])
	}

	scaled := t / c.frameInterval()
	i0 := int(math.Floor(scaled))
	frac := scaled - float64(i0)
	if c.Loop {
		i0 = modInt(i0, n)
		i1 := modInt(i0+1, n)
		return BlendPoses(c.Poses[i0], c.Poses[i1], frac)
	}
	if i0 >= n-1 {
		return clonePose(c.Poses[n-1])
	}
	return BlendPoses(c.Poses[i0], c.Poses[i0+1], frac)
}

// BlendLooping blends two looping clips in phase: each clip is sampled at the
// same normalized phase, so their cycles stay synchronized regardless of
// differing durations. weight 0 yields clipA, weight 1 yields clipB.
func BlendLooping(clipA, clipB Clip, phase, weight float64) LocalPose {
	phase = wrapPositive(phase, 1)
	poseA := SampleClip(clipA, phase*clipA.Duration)
	poseB := SampleClip(clipB, phase*clipB.Duration)
	return BlendPoses(poseA, poseB, clamp(weight, 0, 1))
}

func clonePose(p LocalPose) LocalPose {
	out := NewLocalPose(len(p.Rotations))
	copy(out.Rotations, p.Rotations)
	copy(out.Translations, p.Translations)
	copy(out.Scales, p.Scales)
	return out
}

// wrapPositive maps v into [0, period) even for negative v.
func wrapPositive(v, period float64) float64 {
	if period <= 0 {
		return 0
	}
	return v - period*float64(int64(v/period))
}

func modInt(v, n int) int {
	r := v % n
	if r < 0 {
		r += n
	}
	return r
}
