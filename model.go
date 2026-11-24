package nvmerecov

import (
	"fmt"
	"math"
	"sort"
)

type reqRef struct {
	id    RequestID
	op    Op
	zone  int
	addr  uint64
	count uint64
	data  Blocks
	q     int
	idx   int
	deps  []int
}

type interval struct {
	start uint64
	end   uint64
}

type nzone struct {
	capacity     uint64
	initial      Blocks
	relevant     []interval // all observations
	readRelevant []interval // Read ranges only
}

type nmodel struct {
	queues       [][]int // global request indices, in queue execution order
	reqs         []reqRef
	zones        []nzone
	resetsByZone [][]int
	byID         map[RequestID]int
}

func normalizeModel(input Model) (*nmodel, error) {
	if len(input.Zones) > 3 {
		return nil, fmt.Errorf("%w: at most 3 zones are supported, got %d", ErrInvalidModel, len(input.Zones))
	}
	if len(input.Queues) > 4 {
		return nil, fmt.Errorf("%w: at most 4 queues are supported, got %d", ErrInvalidModel, len(input.Queues))
	}

	m := &nmodel{
		queues: make([][]int, len(input.Queues)),
		byID:   make(map[RequestID]int),
	}
	for zi, zone := range input.Zones {
		initial := Normalize(zone.Initial)
		n, ok := initial.Length()
		if !ok || n > zone.Capacity {
			return nil, fmt.Errorf("%w: zone %d initial content does not fit capacity", ErrInvalidModel, zi)
		}
		m.zones = append(m.zones, nzone{capacity: zone.Capacity, initial: initial})
	}

	requestCount := 0
	for _, queue := range input.Queues {
		requestCount += len(queue.Commands)
	}
	m.reqs = make([]reqRef, 0, requestCount)

	for qi, queue := range input.Queues {
		for idx, command := range queue.Commands {
			if command.Op < OpAppend || command.Op > OpRead {
				return nil, fmt.Errorf("%w: request %d has invalid op %d", ErrInvalidModel, command.ID, command.Op)
			}
			if command.Zone < 0 || command.Zone >= len(m.zones) {
				return nil, fmt.Errorf("%w: request %d has invalid zone %d", ErrInvalidModel, command.ID, command.Zone)
			}
			if _, exists := m.byID[command.ID]; exists {
				return nil, fmt.Errorf("%w: duplicate request id %d", ErrInvalidModel, command.ID)
			}

			ref := reqRef{
				id:   command.ID,
				op:   command.Op,
				zone: command.Zone,
				q:    qi,
				idx:  idx,
			}
			switch command.Op {
			case OpAppend:
				ref.data = Normalize(command.Data)
				if _, ok := ref.data.Length(); !ok {
					return nil, fmt.Errorf("%w: request %d append length overflows", ErrInvalidModel, command.ID)
				}
			case OpRead:
				if command.Addr > math.MaxUint64-command.Count {
					return nil, fmt.Errorf("%w: request %d read range overflows", ErrInvalidModel, command.ID)
				}
				ref.addr = command.Addr
				ref.count = command.Count
				if ref.addr+ref.count > m.zones[ref.zone].capacity {
					return nil, fmt.Errorf("%w: request %d read range exceeds zone capacity", ErrInvalidModel, command.ID)
				}
			case OpReset:
				// No payload.
			}

			m.byID[ref.id] = len(m.reqs)
			m.queues[qi] = append(m.queues[qi], len(m.reqs))
			m.reqs = append(m.reqs, ref)
		}
	}

	// Command-local predecessors and barrier predecessors are resolved only
	// after every ID is known.
	m.resetsByZone = make([][]int, len(m.zones))
	for ri, ref := range m.reqs {
		if ref.op == OpReset {
			m.resetsByZone[ref.zone] = append(m.resetsByZone[ref.zone], ri)
		}
	}

	for qi, queue := range input.Queues {
		for idx, command := range queue.Commands {
			ri := m.queues[qi][idx]
			deps := make([]int, 0, 1+len(command.After))
			if idx > 0 {
				deps = append(deps, m.queues[qi][idx-1])
			}
			for _, id := range command.After {
				dep, ok := m.byID[id]
				if !ok {
					return nil, fmt.Errorf("%w: request %d waits for unknown request %d", ErrUnknownRequest, command.ID, id)
				}
				deps = append(deps, dep)
			}
			m.reqs[ri].deps = dedupInts(deps)
		}

		for _, barrier := range queue.Barriers {
			if barrier.Before < 0 || barrier.Before > len(queue.Commands) {
				return nil, fmt.Errorf("%w: barrier position %d out of range for queue %d", ErrInvalidModel, barrier.Before, qi)
			}
			if barrier.Before == len(queue.Commands) {
				// Nothing follows the barrier in this queue.
				continue
			}
			ri := m.queues[qi][barrier.Before]
			deps := append([]int(nil), m.reqs[ri].deps...)
			for _, id := range barrier.After {
				dep, ok := m.byID[id]
				if !ok {
					return nil, fmt.Errorf("%w: barrier in queue %d waits for unknown request %d", ErrUnknownRequest, qi, id)
				}
				deps = append(deps, dep)
			}
			m.reqs[ri].deps = dedupInts(deps)
		}
	}

	return m, nil
}

type constraints struct {
	want           map[int]Outcome
	mustExecute    map[int]bool
	mustNotExecute map[int]bool
	contradiction  bool
}

type zoneFinal struct {
	zone    int
	wpSet   bool
	wp      uint64
	samples []Sample
	ranges  []ObservedRange
}

func newConstraints() *constraints {
	return &constraints{
		want:           make(map[int]Outcome),
		mustExecute:    make(map[int]bool),
		mustNotExecute: make(map[int]bool),
	}
}

func (c *constraints) addWant(ri int, outcome Outcome) {
	if old, ok := c.want[ri]; ok {
		if !old.Equal(outcome) {
			c.contradiction = true
		}
	} else {
		c.want[ri] = outcome
	}
}

func (c *constraints) addMustExecute(ri int) {
	if c.mustNotExecute[ri] {
		c.contradiction = true
	}
	c.mustExecute[ri] = true
}

func (c *constraints) addMustNotExecute(ri int) {
	if c.mustExecute[ri] {
		c.contradiction = true
	}
	c.mustNotExecute[ri] = true
}

func normalizeObservation(m *nmodel, obs Observation, assumptions []Assumption) (*constraints, []zoneFinal, error) {
	c := newConstraints()
	finals := make([]zoneFinal, 0, len(obs.Zones))

	for _, zoneObs := range obs.Zones {
		if zoneObs.Zone < 0 || zoneObs.Zone >= len(m.zones) {
			return nil, nil, fmt.Errorf("%w: observation names invalid zone %d", ErrInvalidObservation, zoneObs.Zone)
		}
		final := zoneFinal{
			zone:  zoneObs.Zone,
			wpSet: zoneObs.HasWritePointer,
			wp:    zoneObs.WritePointer,
		}
		if final.wpSet && final.wp > m.zones[final.zone].capacity {
			c.contradiction = true
		}
		for _, sample := range zoneObs.Samples {
			if sample.Address >= m.zones[final.zone].capacity {
				return nil, nil, fmt.Errorf("%w: sample address %d exceeds zone %d capacity", ErrInvalidObservation, sample.Address, final.zone)
			}
			final.samples = append(final.samples, Sample{
				Address: sample.Address,
				Block: Block{
					Empty: sample.Block.Empty,
					Value: cloneBytes(sample.Block.Value),
				},
			})
		}
		for _, observedRange := range zoneObs.Ranges {
			blocks := Normalize(observedRange.Blocks)
			length, ok := blocks.Length()
			if !ok || observedRange.Start > math.MaxUint64-length {
				return nil, nil, fmt.Errorf("%w: invalid observed range in zone %d", ErrInvalidObservation, final.zone)
			}
			if observedRange.Start+length > m.zones[final.zone].capacity {
				return nil, nil, fmt.Errorf("%w: observed range exceeds zone %d capacity", ErrInvalidObservation, final.zone)
			}
			final.ranges = append(final.ranges, ObservedRange{
				Start:  observedRange.Start,
				Blocks: blocks,
			})
		}
		finals = append(finals, final)
	}

	for _, receipt := range obs.Receipts {
		ri, ok := m.byID[receipt.Request]
		if !ok {
			return nil, nil, fmt.Errorf("%w: receipt for request %d", ErrUnknownRequest, receipt.Request)
		}
		ref := m.reqs[ri]
		var outcome Outcome
		switch ref.op {
		case OpAppend:
			if !receipt.HasLBA || receipt.HasBlocks {
				return nil, nil, fmt.Errorf("%w: append receipt %d must carry an LBA", ErrInvalidObservation, receipt.Request)
			}
			outcome = AppendOutcome(receipt.LBA)
		case OpRead:
			if !receipt.HasBlocks || receipt.HasLBA {
				return nil, nil, fmt.Errorf("%w: read receipt %d must carry content", ErrInvalidObservation, receipt.Request)
			}
			outcome = ReadOutcome(receipt.Blocks)
		case OpReset:
			if receipt.HasLBA || receipt.HasBlocks {
				return nil, nil, fmt.Errorf("%w: reset receipt %d must not carry a payload", ErrInvalidObservation, receipt.Request)
			}
			outcome = ResetOutcome()
		}
		c.addWant(ri, outcome)
		c.addMustExecute(ri)
	}

	for _, assumption := range assumptions {
		ri, ok := m.byID[assumption.Request]
		if !ok {
			return nil, nil, fmt.Errorf("%w: assumption for request %d", ErrUnknownRequest, assumption.Request)
		}
		ref := m.reqs[ri]
		if assumption.Outcome.Kind == OutcomeNotExecuted {
			c.addMustNotExecute(ri)
			continue
		}
		expectedKind := opOutcomeKind(ref.op)
		if assumption.Outcome.Kind != expectedKind {
			return nil, nil, fmt.Errorf("%w: assumption outcome kind %s does not match %s request %d",
				ErrInvalidObservation, assumption.Outcome.Kind, ref.op, assumption.Request)
		}
		outcome := assumption.Outcome
		if ref.op == OpRead {
			outcome.Blocks = Normalize(outcome.Blocks)
		}
		c.addWant(ri, outcome)
		c.addMustExecute(ri)
	}

	// Build the observation-relevant address intervals. Content outside these
	// intervals can never affect a Read result, sampled block, or image, so it
	// is deliberately forgotten to keep the prefix graph small.
	relevant := make([][]interval, len(m.zones))
	readRelevant := make([][]interval, len(m.zones))
	for _, ref := range m.reqs {
		if ref.op == OpRead && ref.count != 0 {
			iv := interval{start: ref.addr, end: ref.addr + ref.count}
			relevant[ref.zone] = append(relevant[ref.zone], iv)
			readRelevant[ref.zone] = append(readRelevant[ref.zone], iv)
		}
	}
	for _, final := range finals {
		for _, sample := range final.samples {
			relevant[final.zone] = append(relevant[final.zone], interval{start: sample.Address, end: sample.Address + 1})
		}
		for _, observedRange := range final.ranges {
			length := observedRange.Blocks.MustLength()
			if length != 0 {
				relevant[final.zone] = append(relevant[final.zone], interval{
					start: observedRange.Start,
					end:   observedRange.Start + length,
				})
			}
		}
	}
	for zi := range relevant {
		m.zones[zi].relevant = mergeIntervals(relevant[zi])
		m.zones[zi].readRelevant = mergeIntervals(readRelevant[zi])
	}

	return c, finals, nil
}

func mergeIntervals(values []interval) []interval {
	sort.Slice(values, func(i, j int) bool {
		if values[i].start != values[j].start {
			return values[i].start < values[j].start
		}
		return values[i].end < values[j].end
	})
	merged := make([]interval, 0, len(values))
	for _, iv := range values {
		if iv.start >= iv.end {
			continue
		}
		if len(merged) != 0 && iv.start <= merged[len(merged)-1].end {
			if iv.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

func opOutcomeKind(op Op) OutcomeKind {
	switch op {
	case OpAppend:
		return OutcomeAppend
	case OpRead:
		return OutcomeRead
	default:
		return OutcomeReset
	}
}

func dedupInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
