package nvmerecov

import (
	"math/rand"
	"testing"
)

type oracleZone struct {
	wp      uint64
	content Blocks
}

func (z oracleZone) clone() oracleZone {
	return oracleZone{wp: z.wp, content: append(Blocks(nil), z.content...)}
}

func oracleRange(content Blocks, wp, capacity, start, length uint64) (Blocks, bool) {
	if start > ^uint64(0)-length || start+length > capacity {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	end := start + length
	if start >= wp {
		return EmptyBlocks(length), true
	}
	writtenEnd := end
	if writtenEnd > wp {
		writtenEnd = wp
	}
	written, ok := content.Slice(start, writtenEnd-start)
	if !ok {
		return nil, false
	}
	if writtenEnd < end {
		return Join(written, EmptyBlocks(end-writtenEnd)), true
	}
	return written, true
}

func oracleBlock(content Blocks, wp, capacity, address uint64) (Block, bool) {
	if address >= capacity {
		return Block{}, false
	}
	if address >= wp {
		return EmptyBlock(), true
	}
	return content.At(address)
}

func executeOracle(m *nmodel, zones []oracleZone, ref reqRef) (Outcome, bool) {
	z := &zones[ref.zone]
	zone := m.zones[ref.zone]
	switch ref.op {
	case OpAppend:
		length, ok := ref.data.Length()
		if !ok || z.wp > zone.capacity || length > zone.capacity-z.wp {
			return Outcome{}, false
		}
		lba := z.wp
		z.wp += length
		z.content = Join(z.content, ref.data)
		return AppendOutcome(lba), true
	case OpReset:
		z.wp = 0
		z.content = nil
		return ResetOutcome(), true
	case OpRead:
		blocks, ok := oracleRange(z.content, z.wp, zone.capacity, ref.addr, ref.count)
		if !ok {
			return Outcome{}, false
		}
		return ReadOutcome(blocks), true
	default:
		return Outcome{}, false
	}
}

type oracle struct {
	m        *nmodel
	cons     *constraints
	finals   []zoneFinal
	missing  []int
	possible map[int]map[string]Outcome
	valid    bool
}

func newOracle(m *nmodel, cons *constraints, finals []zoneFinal, missing []int) *oracle {
	return &oracle{
		m:        m,
		cons:     cons,
		finals:   finals,
		missing:  missing,
		possible: make(map[int]map[string]Outcome),
	}
}

func (o *oracle) run() {
	zones := make([]oracleZone, len(o.m.zones))
	for zi, zone := range o.m.zones {
		wp, _ := zone.initial.Length()
		zones[zi] = oracleZone{wp: wp, content: zone.initial}
	}
	progress := make([]int, len(o.m.queues))
	results := make(map[int]Outcome)
	o.walk(progress, zones, results)
}

func (o *oracle) walk(progress []int, zones []oracleZone, results map[int]Outcome) {
	if o.breakpoint(progress, zones, results) {
		o.valid = true
		for _, ri := range o.missing {
			ref := o.m.reqs[ri]
			if !executed(progress, ref) {
				if o.possible[ri] == nil {
					o.possible[ri] = make(map[string]Outcome)
				}
				o.possible[ri][outcomeKey(NotExecuted)] = NotExecuted
				continue
			}
			result, ok := results[ri]
			if !ok {
				continue
			}
			if o.possible[ri] == nil {
				o.possible[ri] = make(map[string]Outcome)
			}
			o.possible[ri][outcomeKey(result)] = result
		}
	}

	for qi := range o.m.queues {
		if progress[qi] >= len(o.m.queues[qi]) {
			continue
		}
		ri := o.m.queues[qi][progress[qi]]
		if !oracleDepsDone(o.m, progress, ri) || o.cons.mustNotExecute[ri] {
			continue
		}
		nextZones := make([]oracleZone, len(zones))
		for zi := range zones {
			nextZones[zi] = zones[zi].clone()
		}
		result, ok := executeOracle(o.m, nextZones, o.m.reqs[ri])
		if !ok {
			continue
		}
		if want, exists := o.cons.want[ri]; exists && !want.Equal(result) {
			continue
		}
		nextProgress := append([]int(nil), progress...)
		nextProgress[qi]++
		nextResults := make(map[int]Outcome, len(results)+1)
		for id, value := range results {
			nextResults[id] = value
		}
		nextResults[ri] = result
		o.walk(nextProgress, nextZones, nextResults)
	}
}

func oracleDepsDone(m *nmodel, progress []int, ri int) bool {
	for _, dep := range m.reqs[ri].deps {
		ref := m.reqs[dep]
		if progress[ref.q] <= ref.idx {
			return false
		}
	}
	return true
}

func (o *oracle) breakpoint(progress []int, zones []oracleZone, results map[int]Outcome) bool {
	if o.cons.contradiction {
		return false
	}
	for ri, ref := range o.m.reqs {
		isExecuted := executed(progress, ref)
		if o.cons.mustExecute[ri] && !isExecuted {
			return false
		}
		if o.cons.mustNotExecute[ri] && isExecuted {
			return false
		}
	}
	for _, final := range o.finals {
		z := zones[final.zone]
		zone := o.m.zones[final.zone]
		if final.wpSet && z.wp != final.wp {
			return false
		}
		for _, sample := range final.samples {
			block, ok := oracleBlock(z.content, z.wp, zone.capacity, sample.Address)
			if !ok || !block.Equal(sample.Block) {
				return false
			}
		}
		for _, observedRange := range final.ranges {
			length := observedRange.Blocks.MustLength()
			got, ok := oracleRange(z.content, z.wp, zone.capacity, observedRange.Start, length)
			if !ok || !got.Equal(observedRange.Blocks) {
				return false
			}
		}
	}
	return true
}

func possibleKeys(results []Outcome) map[string]bool {
	out := make(map[string]bool, len(results))
	for _, outcome := range results {
		out[outcomeKey(outcome)] = true
	}
	return out
}

func randomValue(rng *rand.Rand) []byte {
	switch rng.Intn(4) {
	case 0:
		return []byte("A")
	case 1:
		return []byte("B")
	case 2:
		return []byte("C")
	default:
		return []byte{}
	}
}

func TestRandomSmallModelsAgainstBruteForceOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261124))
	const capacity = uint64(5)

	for trial := 0; trial < 200; trial++ {
		queueCount := 2
		requestCount := 4 + rng.Intn(2)
		queues := make([]Queue, queueCount)
		id := RequestID(1)
		for i := 0; i < requestCount; i++ {
			qi := rng.Intn(queueCount)
			command := Command{ID: id, Zone: 0}
			id++
			switch rng.Intn(5) {
			case 0, 1:
				command.Op = OpAppend
				command.Data = Repeat(randomValue(rng), uint64(1+rng.Intn(2)))
			case 2:
				command.Op = OpReset
			default:
				command.Op = OpRead
				command.Addr = uint64(rng.Intn(int(capacity)))
				maxCount := capacity - command.Addr
				command.Count = uint64(1 + rng.Intn(int(maxCount)))
			}
			queues[qi].Commands = append(queues[qi].Commands, command)
		}
		model := Model{Zones: []Zone{{Capacity: capacity}}, Queues: queues}

		// Generate a random legal full order. The breakpoint is a prefix of
		// that order, and all observed receipts are taken from this run.
		m, err := normalizeModel(model)
		if err != nil {
			t.Fatal(err)
		}
		zones := []oracleZone{{}}
		progress := make([]int, len(m.queues))
		results := make(map[int]Outcome)
		states := []struct {
			wp       uint64
			content  Blocks
			progress []int
			results  map[int]Outcome
		}{{
			progress: append([]int(nil), progress...),
			results:  cloneResults(results),
		}}
		for {
			var available []int
			for qi := range m.queues {
				if progress[qi] < len(m.queues[qi]) {
					ri := m.queues[qi][progress[qi]]
					if oracleDepsDone(m, progress, ri) {
						available = append(available, qi)
					}
				}
			}
			if len(available) == 0 {
				break
			}
			qi := available[rng.Intn(len(available))]
			ri := m.queues[qi][progress[qi]]
			result, ok := executeOracle(m, zones, m.reqs[ri])
			if !ok {
				break
			}
			progress[qi]++
			results[ri] = result
			states = append(states, struct {
				wp       uint64
				content  Blocks
				progress []int
				results  map[int]Outcome
			}{
				wp:       zones[0].wp,
				content:  append(Blocks(nil), zones[0].content...),
				progress: append([]int(nil), progress...),
				results:  cloneResults(results),
			})
		}
		breakpoint := rng.Intn(len(states))
		state := states[breakpoint]

		obs := Observation{Zones: []ZoneObservation{{
			Zone:            0,
			HasWritePointer: true,
			WritePointer:    state.wp,
		}}}
		for i := 0; i < 2; i++ {
			address := uint64(rng.Intn(int(capacity)))
			block, _ := oracleBlock(state.content, state.wp, capacity, address)
			obs.Zones[0].Samples = append(obs.Zones[0].Samples, Sample{Address: address, Block: block})
		}
		if rng.Intn(2) == 0 {
			image, _ := oracleRange(state.content, state.wp, capacity, 0, capacity)
			obs.Zones[0].Ranges = append(obs.Zones[0].Ranges, ObservedRange{Start: 0, Blocks: image})
		}
		for ri, result := range state.results {
			if rng.Intn(3) != 0 && executed(state.progress, m.reqs[ri]) {
				obs.Receipts = append(obs.Receipts, receiptFor(m.reqs[ri].id, result))
			}
		}
		if rng.Intn(5) == 0 && len(obs.Receipts) != 0 {
			// Occasionally corrupt one receipt to exercise invalid evidence.
			switch rng.Intn(3) {
			case 0:
				obs.Receipts[0].LBA += 1
			case 1:
				obs.Receipts[0].Blocks = Join(obs.Receipts[0].Blocks, Repeat([]byte("Z"), 1))
			case 2:
				obs.Receipts[0].Blocks = EmptyBlocks(1)
			}
		}

		analysis, err := Analyze(model, obs)
		if err != nil {
			t.Fatal(err)
		}
		cons, finals, err := normalizeObservation(m, obs, nil)
		if err != nil {
			t.Fatal(err)
		}
		receiptSeen := make(map[RequestID]bool)
		for _, receipt := range obs.Receipts {
			receiptSeen[receipt.Request] = true
		}
		var missing []int
		for ri, ref := range m.reqs {
			if !receiptSeen[ref.id] {
				missing = append(missing, ri)
			}
		}
		reference := newOracle(m, cons, finals, missing)
		reference.run()

		if analysis.Valid != reference.valid {
			t.Fatalf("trial %d: validity mismatch: solver=%v oracle=%v", trial, analysis.Valid, reference.valid)
		}
		if !analysis.Valid {
			continue
		}
		verifyOraclePrefix(t, m, cons, finals, analysis.Prefix)
		for _, got := range analysis.Missing {
			ri := m.byID[got.Request]
			want := reference.possible[ri]
			gotKeys := possibleKeys(got.Possible)
			if len(gotKeys) != len(want) {
				t.Fatalf("trial %d request %d: outcome count mismatch: solver=%v oracle=%v", trial, got.Request, got.Possible, want)
			}
			for key := range want {
				if !gotKeys[key] {
					t.Fatalf("trial %d request %d: missing oracle outcome %q: solver=%v", trial, got.Request, key, got.Possible)
				}
			}
			for _, witness := range got.Witnesses {
				verifyOraclePrefix(t, m, cons, finals, witness)
			}
		}
		for _, witness := range analysis.Witnesses {
			verifyOraclePrefix(t, m, cons, finals, witness)
		}

		// Exercise joint queries against the same brute-force oracle.
		if len(analysis.Missing) != 0 {
			count := 1
			if len(analysis.Missing) > 1 && rng.Intn(2) == 0 {
				count = 2
			}
			assumptions := make([]Assumption, 0, count)
			for i := 0; i < count; i++ {
				entry := analysis.Missing[rng.Intn(len(analysis.Missing))]
				if len(entry.Possible) == 0 {
					continue
				}
				outcome := entry.Possible[rng.Intn(len(entry.Possible))]
				if rng.Intn(5) == 0 && outcome.Kind == OutcomeAppend {
					outcome.LBA++
				}
				assumptions = append(assumptions, Assumption{Request: entry.Request, Outcome: outcome})
			}
			joint, err := QueryJoint(model, obs, assumptions)
			if err != nil {
				t.Fatal(err)
			}
			jointCons, jointFinals, err := normalizeObservation(m, obs, assumptions)
			if err != nil {
				t.Fatal(err)
			}
			jointOracle := newOracle(m, jointCons, jointFinals, nil)
			jointOracle.run()
			if joint.Satisfiable != jointOracle.valid {
				t.Fatalf("trial %d: joint mismatch: solver=%v oracle=%v assumptions=%+v",
					trial, joint.Satisfiable, jointOracle.valid, assumptions)
			}
			if joint.Satisfiable {
				verifyOraclePrefix(t, m, jointCons, jointFinals, joint.Prefix)
			}
		}
	}
}

func verifyOraclePrefix(t *testing.T, m *nmodel, cons *constraints, finals []zoneFinal, prefix Prefix) {
	t.Helper()
	zones := make([]oracleZone, len(m.zones))
	for zi, zone := range m.zones {
		wp, _ := zone.initial.Length()
		zones[zi] = oracleZone{wp: wp, content: zone.initial}
	}
	progress := make([]int, len(m.queues))
	for _, step := range prefix.Steps {
		ri, ok := m.byID[step.Request]
		if !ok {
			t.Fatalf("prefix references unknown request %d", step.Request)
		}
		ref := m.reqs[ri]
		if progress[ref.q] != ref.idx {
			t.Fatalf("request %d is not next in its queue", step.Request)
		}
		if !oracleDepsDone(m, progress, ri) {
			t.Fatalf("request %d executes before its barrier predecessors", step.Request)
		}
		result, ok := executeOracle(m, zones, ref)
		if !ok {
			t.Fatalf("request %d cannot execute in witness prefix", step.Request)
		}
		if !result.Equal(step.Result) {
			t.Fatalf("request %d witness result %v, replay result %v", step.Request, step.Result, result)
		}
		progress[ref.q]++
	}
	reference := newOracle(m, cons, finals, nil)
	if !reference.breakpoint(progress, zones, nil) {
		t.Fatalf("witness prefix does not reach the observed breakpoint")
	}
}

func cloneResults(results map[int]Outcome) map[int]Outcome {
	out := make(map[int]Outcome, len(results))
	for id, result := range results {
		out[id] = result
	}
	return out
}

func receiptFor(id RequestID, result Outcome) Receipt {
	switch result.Kind {
	case OutcomeAppend:
		return Receipt{Request: id, HasLBA: true, LBA: result.LBA}
	case OutcomeRead:
		return Receipt{Request: id, HasBlocks: true, Blocks: result.Blocks}
	default:
		return Receipt{Request: id}
	}
}
