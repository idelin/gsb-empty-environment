package nvmerecov

// finalPrefixOK is a sound forward-pruning rule. When no Reset on a zone can
// still execute, all future operations on that zone are appends. Consequently
// the already-written portion of the zone can never change before the
// breakpoint and must already agree with every observed sample, image range,
// and the final write pointer. This prevents the search from enumerating
// different append interleavings that already contradict a sealed image.
func (g *graph) finalPrefixOK(progress []int, zones []zstate) bool {
	for _, final := range g.finals {
		zone := g.m.zones[final.zone]
		var resetPossible bool
		for _, ri := range g.m.resetsByZone[final.zone] {
			ref := g.m.reqs[ri]
			if !executed(progress, ref) && !g.cons.mustNotExecute[ri] {
				resetPossible = true
				break
			}
		}
		if resetPossible {
			continue
		}

		z := &zones[final.zone]
		if final.wpSet && z.wp > final.wp {
			return false
		}
		for _, sample := range final.samples {
			if sample.Address >= z.wp {
				// A later append can still fill this address.
				continue
			}
			block, ok := z.blockAt(zone.capacity, sample.Address)
			if !ok || !block.Equal(sample.Block) {
				return false
			}
		}
		for _, observedRange := range final.ranges {
			length := observedRange.Blocks.MustLength()
			if z.wp <= observedRange.Start {
				continue
			}
			fixedLength := length
			if observedRange.Start+length > z.wp {
				fixedLength = z.wp - observedRange.Start
			}
			if fixedLength == 0 {
				continue
			}
			got, ok := z.rangeAt(zone.capacity, observedRange.Start, fixedLength)
			if !ok {
				return false
			}
			want, ok := observedRange.Blocks.Slice(0, fixedLength)
			if !ok || !blocksEqual(got, want) {
				return false
			}
		}
	}
	return true
}

// pendingReadPrefixOK applies the same monotonicity argument to a successful
// Read that must still execute. With no remaining possible Reset on its zone,
// the already-written part of the Read range can no longer change, so it must
// already agree with the surviving receipt.
func (g *graph) pendingReadPrefixOK(progress []int, zones []zstate) bool {
	for zi := range zones {
		var resetPossible bool
		for _, ri := range g.m.resetsByZone[zi] {
			ref := g.m.reqs[ri]
			if !executed(progress, ref) && !g.cons.mustNotExecute[ri] {
				resetPossible = true
				break
			}
		}
		if resetPossible {
			continue
		}

		z := &zones[zi]
		zone := g.m.zones[zi]
		for ri, ref := range g.m.reqs {
			if ref.zone != zi || ref.op != OpRead || executed(progress, ref) {
				continue
			}
			if !g.cons.mustExecute[ri] {
				continue
			}
			want, ok := g.cons.want[ri]
			if !ok || want.Kind != OutcomeRead {
				continue
			}
			if z.wp <= ref.addr {
				continue
			}
			fixedLength := ref.count
			if ref.addr+ref.count > z.wp {
				fixedLength = z.wp - ref.addr
			}
			if fixedLength == 0 {
				continue
			}
			got, ok := z.rangeAt(zone.capacity, ref.addr, fixedLength)
			if !ok {
				return false
			}
			expected, ok := want.Blocks.Slice(0, fixedLength)
			if !ok || !blocksEqual(got, expected) {
				return false
			}
		}
	}
	return true
}
