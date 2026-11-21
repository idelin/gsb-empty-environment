package gatetable

import "fmt"

// Compile builds an optimal periodic gate table.  It minimizes the maximum
// per-frame completion latency, then minimizes the offsets lexicographically
// in frame input order and, within a frame, in the order of Ports.
func Compile(p Problem) (*Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	s, err := newSolver(p)
	if err != nil {
		return nil, err
	}
	offsetsByVar, latency, err := s.solve()
	if err != nil {
		return nil, err
	}

	res := &Result{
		Period:         p.Period,
		Frames:         make([]FramePlan, len(p.Frames)),
		Offsets:        make([][]int, len(p.Frames)),
		FrameLatencies: make([]int, len(p.Frames)),
		Latency:        latency,
		problem:        p.clone(),
	}
	for fi, frame := range p.Frames {
		plan := FramePlan{
			Frame:   fi,
			Name:    frame.Name,
			Offsets: make([]int, len(frame.Ports)),
			Copies:  make([]Copy, len(frame.Ports)),
		}
		for i, port := range frame.Ports {
			o := offsetsByVar[s.base[fi]+i]
			d := frame.Durations[i]
			cp := Copy{
				Frame:    fi,
				Port:     port,
				Offset:   o,
				Start:    (frame.Release + o) % p.Period,
				Duration: d,
			}
			cp.End = cp.Start + d
			plan.Copies[i] = cp
			plan.Offsets[i] = o
			if o+d > plan.Latency {
				plan.Latency = o + d
			}
		}
		res.Frames[fi] = plan
		res.Offsets[fi] = append([]int(nil), plan.Offsets...)
		res.FrameLatencies[fi] = plan.Latency
	}

	busy := make([]bitset, len(p.Ports))
	for fi, frame := range p.Frames {
		for i, port := range frame.Ports {
			o := res.Frames[fi].Offsets[i]
			phase := (frame.Release + o) % p.Period
			busy[port].or(circularBitMask(phase, frame.Durations[i], p.Period))
		}
	}
	res.Masks = make([]uint8, p.Period)
	for t := 0; t < p.Period; t++ {
		for port := range busy {
			if busy[port].test(t) {
				res.Masks[t] |= uint8(1) << uint(port)
			}
		}
	}
	res.SlotsUsed = peakSlots(p.Period, releasesOf(p), res.FrameLatencies)
	res.TableEntries = tableEntries(res.Masks)
	res.Spans = compressMasks(res.Masks)
	res.PortTableEntries = make([]int, len(busy))
	for port := range busy {
		res.PortTableEntries[port] = countPortEntries(busy[port], p.Period)
	}

	if p.MaxSlots != Unlimited && res.SlotsUsed > p.MaxSlots {
		return nil, fmt.Errorf("%w: computed %d slots exceeds limit %d",
			ErrUnschedulable, res.SlotsUsed, p.MaxSlots)
	}
	if p.MaxTableEntries != Unlimited && res.TableEntries > p.MaxTableEntries {
		return nil, fmt.Errorf("%w: computed %d table entries exceeds limit %d",
			ErrUnschedulable, res.TableEntries, p.MaxTableEntries)
	}
	return res, nil
}
