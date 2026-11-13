package netsec

import (
	"fmt"
	"sort"
)

// Policy is the target device policy: a finite set of allowed device
// permissions plus a mandatory deny set that always wins.
type Policy struct {
	Allow []DevicePerm
	Deny  []DevicePerm
}

func (p *Policy) valid() error {
	for _, dp := range p.Allow {
		if err := dp.Device.valid(); err != nil {
			return fmt.Errorf("allow entry %v: %w", dp, err)
		}
	}
	for _, dp := range p.Deny {
		if err := dp.Device.valid(); err != nil {
			return fmt.Errorf("deny entry %v: %w", dp, err)
		}
	}
	return nil
}

// Report is the outcome of verifying a compiled diagram against a Policy.
type Report struct {
	// Sound is true when every granted permission belongs to the target
	// allow set and none hits the mandatory deny set.
	Sound bool
	// Violation is the minimal over-granted device permission, ordered by
	// type (c<b), major, minor, then permission bit. Nil when sound.
	Violation *DevicePerm
	// Complete is true when every target-allowed permission is granted.
	Complete bool
	// Missing is the minimal target-allowed permission the rules deny,
	// in the same canonical order. Nil when complete.
	Missing *DevicePerm
}

func devicePermLess(x, y DevicePerm) bool {
	if deviceLess(x.Device, y.Device) {
		return true
	}
	if deviceLess(y.Device, x.Device) {
		return false
	}
	return x.Perms < y.Perms
}

// Verify checks a compiled diagram against the target policy. It works on
// symbolic regions and the finite policy sets only; it never enumerates the
// full 2 x 4096 x 4096 device space.
func Verify(d *Diagram, p *Policy) (*Report, error) {
	if err := p.valid(); err != nil {
		return nil, err
	}
	rep := &Report{Sound: true, Complete: true}

	var violation *DevicePerm
	considerViolation := func(dp DevicePerm) {
		if violation == nil || devicePermLess(dp, *violation) {
			cp := dp
			violation = &cp
		}
	}

	for _, rg := range d.Regions {
		if rg.Perms == 0 {
			continue
		}
		for _, bit := range permBits {
			if rg.Perms&bit == 0 {
				continue
			}
			// Mandatory deny entries inside the region are violations.
			if dp, ok := minPointInRegion(p.Deny, bit, rg); ok {
				considerViolation(DevicePerm{dp, bit})
			}
			// Any device in the region not covered by the finite allow
			// set is over-granted; find the minimal one via first gap.
			if gap, ok := firstGap(rg, allowPoints(p.Allow, bit, rg)); ok {
				considerViolation(DevicePerm{gap, bit})
			}
		}
	}
	if violation != nil {
		rep.Sound = false
		rep.Violation = violation
	}

	var missing *DevicePerm
	for _, dp := range p.Allow {
		lacking := dp.Perms &^ d.Query(dp.Device)
		for _, bit := range permBits {
			if lacking&bit == 0 {
				continue
			}
			cand := DevicePerm{dp.Device, bit}
			if missing == nil || devicePermLess(cand, *missing) {
				cp := cand
				missing = &cp
			}
		}
	}
	if missing != nil {
		rep.Complete = false
		rep.Missing = missing
	}
	return rep, nil
}

// minPointInRegion returns the minimal device of a policy set that carries
// the given permission bit and lies inside the region.
func minPointInRegion(set []DevicePerm, bit Perm, rg Region) (Device, bool) {
	var best *Device
	for _, dp := range set {
		if dp.Perms&bit == 0 || !rg.contains(dp.Device) {
			continue
		}
		if best == nil || deviceLess(dp.Device, *best) {
			d := dp.Device
			best = &d
		}
	}
	if best == nil {
		return Device{}, false
	}
	return *best, true
}

// allowPoints collects, sorted and deduplicated, the allowed devices inside
// the region that carry the given permission bit.
func allowPoints(allow []DevicePerm, bit Perm, rg Region) []Device {
	seen := make(map[Device]struct{})
	var pts []Device
	for _, dp := range allow {
		if dp.Perms&bit == 0 || !rg.contains(dp.Device) {
			continue
		}
		if _, ok := seen[dp.Device]; ok {
			continue
		}
		seen[dp.Device] = struct{}{}
		pts = append(pts, dp.Device)
	}
	sort.Slice(pts, func(i, j int) bool { return deviceLess(pts[i], pts[j]) })
	return pts
}

// firstGap returns the minimal device inside the region (in major/minor
// order) that is not present in the sorted point set. It walks at most
// len(points)+1 devices, never the whole region.
func firstGap(rg Region, points []Device) (Device, bool) {
	cur := Device{Type: rg.Type, Major: rg.MajorLo, Minor: rg.MinorLo}
	i := 0
	for i < len(points) && points[i] == cur {
		i++
		if cur.Minor < rg.MinorHi {
			cur.Minor++
		} else {
			cur.Major++
			cur.Minor = rg.MinorLo
		}
		if cur.Major > rg.MajorHi {
			return Device{}, false
		}
	}
	return cur, true
}

// SemanticDiff is the first device, in canonical order, whose granted
// permissions differ between two compiled diagrams.
type SemanticDiff struct {
	Device
	Before Perm
	After  Perm
}

func (sd SemanticDiff) String() string {
	return fmt.Sprintf("%v: before=%v after=%v", sd.Device, sd.Before, sd.After)
}

// CompareDiagrams finds the first semantic difference between two compiled
// diagrams by intersecting their symbolic regions. Nil means equivalent.
func CompareDiagrams(before, after *Diagram) *SemanticDiff {
	var best *SemanticDiff
	for _, ra := range before.Regions {
		for _, rb := range after.Regions {
			if ra.Type != rb.Type || ra.Perms == rb.Perms {
				continue
			}
			majLo, majHi := max(ra.MajorLo, rb.MajorLo), min(ra.MajorHi, rb.MajorHi)
			minLo, minHi := max(ra.MinorLo, rb.MinorLo), min(ra.MinorHi, rb.MinorHi)
			if majLo > majHi || minLo > minHi {
				continue
			}
			cand := SemanticDiff{Device{ra.Type, majLo, minLo}, ra.Perms, rb.Perms}
			if best == nil || deviceLess(cand.Device, best.Device) {
				cp := cand
				best = &cp
			}
		}
	}
	return best
}

// ReviewOptimizer compiles the rule set before and after a pending
// optimizer and returns the first semantic difference between all symbolic
// regions, or nil when the optimizer preserves semantics.
func ReviewOptimizer(rules []Rule, optimize func([]Rule) []Rule) (*SemanticDiff, error) {
	before, err := Compile(rules)
	if err != nil {
		return nil, err
	}
	after, err := Compile(optimize(append([]Rule(nil), rules...)))
	if err != nil {
		return nil, fmt.Errorf("optimized rules: %w", err)
	}
	return CompareDiagrams(before, after), nil
}
