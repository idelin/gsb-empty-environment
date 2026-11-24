package nvmerecov

import (
	"math"
	"testing"
)

func appendRequest(id RequestID, queueZone int, value string) Command {
	return Command{
		ID:   id,
		Op:   OpAppend,
		Zone: queueZone,
		Data: Repeat([]byte(value), 1),
	}
}

func findMissing(t *testing.T, analysis *Analysis, id RequestID) MissingResult {
	t.Helper()
	for _, missing := range analysis.Missing {
		if missing.Request == id {
			return missing
		}
	}
	t.Fatalf("request %d is not in missing report", id)
	return MissingResult{}
}

func hasOutcome(outcomes []Outcome, want Outcome) bool {
	for _, outcome := range outcomes {
		if outcome.Equal(want) {
			return true
		}
	}
	return false
}

func TestIdenticalAppendsCanSwapLBA(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 8}},
		Queues: []Queue{
			{Commands: []Command{appendRequest(1, 0, "A")}},
			{Commands: []Command{appendRequest(2, 0, "A")}},
		},
	}
	obs := Observation{
		Zones: []ZoneObservation{{Zone: 0, HasWritePointer: true, WritePointer: 2}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("observations must be explainable: %s", analysis.Reason)
	}

	for _, id := range []RequestID{1, 2} {
		missing := findMissing(t, analysis, id)
		if !hasOutcome(missing.Possible, AppendOutcome(0)) || !hasOutcome(missing.Possible, AppendOutcome(1)) {
			t.Fatalf("request %d must have both LBA 0 and 1: %v", id, missing.Possible)
		}
		if len(missing.Possible) != 2 {
			t.Fatalf("request %d should have exactly two outcomes, got %v", id, missing.Possible)
		}
		if missing.Publishable {
			t.Fatalf("request %d has non-unique LBA and must not be published", id)
		}
		if len(missing.Witnesses) != 2 {
			t.Fatalf("request %d needs two independently replayable witnesses", id)
		}
	}
	if len(analysis.Witnesses) != 2 {
		t.Fatalf("analysis must expose two contrasting prefixes, got %d", len(analysis.Witnesses))
	}
}

func TestFinalEmptyImageButOldReadDistinguishesHistory(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 4}},
		Queues: []Queue{
			{
				Commands: []Command{
					appendRequest(1, 0, "A"),
					{ID: 2, Op: OpRead, Zone: 0, Addr: 0, Count: 1},
					{ID: 3, Op: OpReset, Zone: 0},
				},
				// Reset cannot run before the B append on queue 1.
				Barriers: []Barrier{{Before: 2, After: []RequestID{4}}},
			},
			{Commands: []Command{appendRequest(4, 0, "B")}},
		},
	}
	obs := Observation{
		Zones: []ZoneObservation{{Zone: 0, HasWritePointer: true, WritePointer: 0}},
		Receipts: []Receipt{{
			Request:   2,
			HasBlocks: true,
			Blocks:    Repeat([]byte("B"), 1),
		}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("read B then reset is a legal history: %s", analysis.Reason)
	}

	want := map[RequestID]Outcome{
		1: AppendOutcome(1), // B at 0, A at 1
		3: ResetOutcome(),   // required to erase both writes
		4: AppendOutcome(0), // B must precede A for the sampled read
	}
	for id, outcome := range want {
		missing := findMissing(t, analysis, id)
		if len(missing.Possible) != 1 || !missing.Possible[0].Equal(outcome) {
			t.Fatalf("request %d: got %v, want only %v", id, missing.Possible, outcome)
		}
		if !missing.Publishable {
			t.Fatalf("request %d has a unique outcome and should be publishable", id)
		}
	}
}

func TestResetOnAnotherQueueBlockedByBarrier(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 8}},
		Queues: []Queue{
			{
				Commands: []Command{
					appendRequest(1, 0, "X"),
					appendRequest(2, 0, "Y"),
				},
				// Y cannot run until the Reset on queue 1 has completed.
				Barriers: []Barrier{{Before: 1, After: []RequestID{3}}},
			},
			{Commands: []Command{{
				ID:    3,
				Op:    OpReset,
				Zone:  0,
				After: []RequestID{1},
			}}},
		},
	}
	obs := Observation{
		Zones: []ZoneObservation{{
			Zone:            0,
			HasWritePointer: true,
			WritePointer:    1,
			Samples: []Sample{{
				Address: 0,
				Block:   DataBlock([]byte("Y")),
			}},
		}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("X, Reset, Y is a legal history: %s", analysis.Reason)
	}

	want := map[RequestID]Outcome{
		1: AppendOutcome(0),
		3: ResetOutcome(),
		2: AppendOutcome(0), // after the Reset
	}
	for id, outcome := range want {
		missing := findMissing(t, analysis, id)
		if len(missing.Possible) != 1 || !missing.Possible[0].Equal(outcome) {
			t.Fatalf("request %d: got %v, want only %v", id, missing.Possible, outcome)
		}
	}
}

func TestReadValidatesLastBlockOfLongRepeat(t *testing.T) {
	const prefixBlocks = uint64(1) << 40
	capacity := prefixBlocks + 2
	data := Join(Repeat([]byte("A"), prefixBlocks), Repeat([]byte("B"), 1))

	model := Model{
		Zones: []Zone{{Capacity: capacity}},
		Queues: []Queue{{Commands: []Command{
			{ID: 1, Op: OpAppend, Zone: 0, Data: data},
			{ID: 2, Op: OpRead, Zone: 0, Addr: 0, Count: prefixBlocks + 1},
		}}},
	}

	badRead := Join(Repeat([]byte("A"), prefixBlocks), Repeat([]byte("C"), 1))
	bad := Observation{Receipts: []Receipt{{
		Request:   2,
		HasBlocks: true,
		Blocks:    badRead,
	}}}
	analysis, err := Analyze(model, bad)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Valid {
		t.Fatal("read differing only in the last block must not match")
	}

	goodRead := Join(Repeat([]byte("A"), prefixBlocks), Repeat([]byte("B"), 1))
	good := Observation{
		Zones: []ZoneObservation{{
			Zone:            0,
			HasWritePointer: true,
			WritePointer:    prefixBlocks + 1,
			Samples: []Sample{{
				Address: prefixBlocks,
				Block:   DataBlock([]byte("B")),
			}},
		}},
		Receipts: []Receipt{{
			Request:   2,
			HasBlocks: true,
			Blocks:    goodRead,
		}},
	}
	analysis, err = Analyze(model, good)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("exact long read must match: %s", analysis.Reason)
	}
	missing := findMissing(t, analysis, 1)
	if len(missing.Possible) != 1 || !missing.Possible[0].Equal(AppendOutcome(0)) {
		t.Fatalf("append result: got %v, want LBA 0", missing.Possible)
	}
}

func TestJointQueryRejectsIndividuallyLegalButIncompatibleReceipts(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 8}},
		Queues: []Queue{
			{Commands: []Command{appendRequest(1, 0, "A")}},
			{Commands: []Command{appendRequest(2, 0, "A")}},
		},
	}
	obs := Observation{
		Zones: []ZoneObservation{{Zone: 0, HasWritePointer: true, WritePointer: 2}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatal("setup must be valid")
	}
	for _, id := range []RequestID{1, 2} {
		missing := findMissing(t, analysis, id)
		if !hasOutcome(missing.Possible, AppendOutcome(0)) {
			t.Fatalf("request %d must individually admit LBA 0", id)
		}
	}

	impossible := []Assumption{
		{Request: 1, Outcome: AppendOutcome(0)},
		{Request: 2, Outcome: AppendOutcome(0)},
	}
	joint, err := QueryJoint(model, obs, impossible)
	if err != nil {
		t.Fatal(err)
	}
	if joint.Satisfiable {
		t.Fatal("two appends cannot both start at LBA 0 in one prefix")
	}

	possible := []Assumption{
		{Request: 1, Outcome: AppendOutcome(0)},
		{Request: 2, Outcome: AppendOutcome(1)},
	}
	joint, err = QueryJoint(model, obs, possible)
	if err != nil {
		t.Fatal(err)
	}
	if !joint.Satisfiable {
		t.Fatalf("compatible assumptions must have one shared witness: %s", joint.Reason)
	}
	if len(joint.Prefix.Steps) != 2 {
		t.Fatalf("joint witness must execute both requests, got %d steps", len(joint.Prefix.Steps))
	}
}

func TestCapacityExceededIsNotMovedToAnotherZone(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 1}, {Capacity: 5}},
		Queues: []Queue{{Commands: []Command{{
			ID:   1,
			Op:   OpAppend,
			Zone: 0,
			Data: Repeat([]byte("X"), 2),
		}}}},
	}
	obs := Observation{Zones: []ZoneObservation{
		{Zone: 0, HasWritePointer: true, WritePointer: 0},
		{Zone: 1, HasWritePointer: true, WritePointer: 0},
	}}
	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("the append may simply remain unexecuted: %s", analysis.Reason)
	}
	missing := findMissing(t, analysis, 1)
	if len(missing.Possible) != 1 || missing.Possible[0].Kind != OutcomeNotExecuted {
		t.Fatalf("over-capacity append must be unexecuted, got %v", missing.Possible)
	}
	if !missing.Deterministic {
		t.Fatal("a deterministic non-execution must be reported as deterministic")
	}
	if missing.Publishable {
		t.Fatal("there is no executed receipt to publish")
	}

	obs.Receipts = []Receipt{{Request: 1, HasLBA: true, LBA: 0}}
	analysis, err = Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Valid {
		t.Fatal("an observed successful over-capacity append cannot be explained")
	}
}

func TestEmptyMarkerIsDistinctFromEmptyWrittenValue(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: 4}},
		Queues: []Queue{{Commands: []Command{
			{ID: 1, Op: OpAppend, Zone: 0, Data: Repeat([]byte{}, 1)},
			{ID: 2, Op: OpRead, Zone: 0, Addr: 0, Count: 1},
		}}},
	}
	obs := Observation{Receipts: []Receipt{{
		Request:   2,
		HasBlocks: true,
		Blocks:    Repeat([]byte{}, 1),
	}}}
	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("an empty byte value is different from the empty marker: %s", analysis.Reason)
	}

	emptyRead := Observation{Receipts: []Receipt{{
		Request:   2,
		HasBlocks: true,
		Blocks:    EmptyBlocks(1),
	}}}
	analysis, err = Analyze(model, emptyRead)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Valid {
		t.Fatal("empty marker must not compare equal to a written empty byte value")
	}
}

func TestBlocksCompressedEqualityAndSlicing(t *testing.T) {
	const n = uint64(1) << 45
	long := Repeat([]byte("same"), n)
	split := Join(Repeat([]byte("same"), n/3), Repeat([]byte("same"), n-n/3))
	if !long.Equal(split) {
		t.Fatal("run boundaries must not affect content equality")
	}

	withTail := Join(long, Repeat([]byte("tail"), 1))
	got, ok := withTail.Slice(n-1, 2)
	if !ok {
		t.Fatal("slice failed")
	}
	want := Join(Repeat([]byte("same"), 1), Repeat([]byte("tail"), 1))
	if !got.Equal(want) {
		t.Fatalf("slice content mismatch: %v vs %v", got, want)
	}

	if (Block{}).Equal(EmptyBlock()) {
		t.Fatal("zero written block must not equal empty marker")
	}
}

func TestOverflowingReadRangeIsRejected(t *testing.T) {
	model := Model{
		Zones: []Zone{{Capacity: math.MaxUint64}},
		Queues: []Queue{{Commands: []Command{{
			ID:    1,
			Op:    OpRead,
			Zone:  0,
			Addr:  math.MaxUint64,
			Count: 2,
		}}}},
	}
	if _, err := Analyze(model, Observation{}); err == nil {
		t.Fatal("overflowing read range must be rejected")
	}
}
