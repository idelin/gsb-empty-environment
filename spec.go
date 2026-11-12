package netsec

import "errors"

// RegionSpec describes one protected region in human-facing terms.
type RegionSpec struct {
	// Name is optional metadata echoed back on planned writes.
	Name string

	// StartEnable is the region's current enable (writable permission) bit.
	StartEnable bool
	// StartLock is the region's current lock bit.
	StartLock bool

	// TargetEnable is the desired enable bit.
	TargetEnable bool
	// TargetLock requests the region be locked. It defaults to true through
	// BuildModel: a permission plan normally has to seal every region, since an
	// unlocked enable can be flipped by later software.
	TargetLock bool

	// Forbidden marks the region's enable bit as a never-open sensitive area.
	// Every state the planner visits must keep this bit cleared.
	Forbidden bool

	// IgnoreTarget excludes the region from goal matching when true, leaving
	// its final value unspecified.
	IgnoreTarget bool
}

// RegionWrite annotates a planned Write with the regions it touches.
type RegionWrite struct {
	Write   Write
	Regions []int
}

// BuildModel compiles a region list into the bit-level Model. All regions are
// treated as locked targets unless RegionSpec.IgnoreTarget is set.
func BuildModel(regions []RegionSpec, singleField bool) (Model, error) {
	if len(regions) == 0 {
		return Model{}, errors.New("netsec: at least one region is required")
	}
	if 2*len(regions) > MaxMutableBits {
		return Model{}, errors.New("netsec: more than 24 mutable bits")
	}

	var start, target, targetMask, forbidden uint32
	for r, spec := range regions {
		enableBit := uint32(1) << EnableBit(r)
		lockBit := uint32(1) << LockBit(r)

		if spec.StartEnable {
			start |= enableBit
		}
		if spec.StartLock {
			start |= lockBit
		}
		if spec.Forbidden {
			forbidden |= enableBit
		}
		if spec.IgnoreTarget {
			continue
		}
		targetMask |= enableBit | lockBit
		if spec.TargetEnable {
			target |= enableBit
		}
		if spec.TargetLock {
			target |= lockBit
		}
	}

	m := Model{
		Regions:       len(regions),
		Start:         start,
		Target:        target,
		TargetMask:    targetMask,
		ForbiddenMask: forbidden,
		SingleField:   singleField,
	}
	if err := m.Validate(); err != nil {
		return Model{}, err
	}
	return m, nil
}

// PlanRegions compiles the region list and returns its shortest safe write
// sequence.
func PlanRegions(regions []RegionSpec, singleField bool, budget Budget) Plan {
	m, err := BuildModel(regions, singleField)
	if err != nil {
		return Plan{Status: StatusInvalid, Err: err, Writes: []Write{}}
	}
	return PlanModel(m, budget)
}

// Annotate maps each write's mask back to the region indices it modifies.
func Annotate(m Model, p Plan) []RegionWrite {
	out := make([]RegionWrite, 0, len(p.Writes))
	for _, w := range p.Writes {
		touched := make([]int, 0, m.Regions)
		for r := 0; r < m.Regions; r++ {
			regionMask := uint32(1)<<EnableBit(r) | uint32(1)<<LockBit(r)
			if w.WriteMask&regionMask != 0 {
				touched = append(touched, r)
			}
		}
		out = append(out, RegionWrite{Write: w, Regions: touched})
	}
	return out
}

// Simulate replays writes from start and returns the final state. It lets the
// host double-check that the produced transactions apply as intended on its
// register layout.
func Simulate(m Model, writes []Write) uint32 {
	state := m.Start
	for _, w := range writes {
		state = Apply(state, w, m.Regions)
	}
	return state
}
