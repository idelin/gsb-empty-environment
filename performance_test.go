package nvmerecov

import (
	"fmt"
	"testing"
)

func TestThirtyDistinctAppendsWithSealedFullImage(t *testing.T) {
	const zoneCapacity = 30
	queueLengths := []int{8, 8, 7, 7}
	queues := make([]Queue, len(queueLengths))
	ids := make([][]RequestID, len(queueLengths))
	nextID := RequestID(1)
	values := make([][]string, len(queueLengths))
	for qi, length := range queueLengths {
		ids[qi] = make([]RequestID, length)
		values[qi] = make([]string, length)
		for idx := 0; idx < length; idx++ {
			id := nextID
			nextID++
			value := fmt.Sprintf("q%d-%d", qi, idx)
			ids[qi][idx] = id
			values[qi][idx] = value
			queues[qi].Commands = append(queues[qi].Commands, Command{
				ID:   id,
				Op:   OpAppend,
				Zone: 0,
				Data: Repeat([]byte(value), 1),
			})
		}
	}

	// A deterministic interleaving is the only order compatible with the
	// sealed image because every appended value is different.
	var final Blocks
	var positions []int
	expectedLBA := make(map[RequestID]int)
	for round := 0; ; round++ {
		progressed := false
		for qi := range queueLengths {
			if round >= queueLengths[qi] {
				continue
			}
			progressed = true
			final = Join(final, Repeat([]byte(values[qi][round]), 1))
			positions = append(positions, qi)
			expectedLBA[ids[qi][round]] = len(positions) - 1
		}
		if !progressed {
			break
		}
	}

	model := Model{
		Zones:  []Zone{{Capacity: zoneCapacity}},
		Queues: queues,
	}
	obs := Observation{Zones: []ZoneObservation{{
		Zone:            0,
		HasWritePointer: true,
		WritePointer:    uint64(len(positions)),
		Ranges: []ObservedRange{{
			Start:  0,
			Blocks: final,
		}},
	}}}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("the chosen interleaving must be legal: %s", analysis.Reason)
	}
	if len(analysis.Missing) != len(positions) {
		t.Fatalf("missing count = %d, want %d", len(analysis.Missing), len(positions))
	}
	for _, missing := range analysis.Missing {
		want := AppendOutcome(uint64(expectedLBA[missing.Request]))
		if len(missing.Possible) != 1 || !missing.Possible[0].Equal(want) {
			t.Fatalf("request %d: got %v, want only %v", missing.Request, missing.Possible, want)
		}
	}
}

func TestMandatoryResetMakesPreResetImageIrrelevant(t *testing.T) {
	const zoneCapacity = 30
	queueLengths := []int{8, 8, 7, 7}
	queues := make([]Queue, len(queueLengths))
	nextID := RequestID(1)
	for qi, length := range queueLengths {
		for idx := 0; idx < length; idx++ {
			value := fmt.Sprintf("q%d-%d", qi, idx)
			queues[qi].Commands = append(queues[qi].Commands, Command{
				ID:   nextID,
				Op:   OpAppend,
				Zone: 0,
				Data: Repeat([]byte(value), 1),
			})
			nextID++
		}
	}
	resetID := nextID
	queues[0].Commands = append(queues[0].Commands, Command{ID: resetID, Op: OpReset, Zone: 0})

	model := Model{Zones: []Zone{{Capacity: zoneCapacity}}, Queues: queues}
	obs := Observation{
		Zones: []ZoneObservation{{
			Zone:            0,
			HasWritePointer: true,
			WritePointer:    0,
			Ranges: []ObservedRange{{
				Start:  0,
				Blocks: EmptyBlocks(zoneCapacity),
			}},
		}},
		Receipts: []Receipt{{Request: resetID}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("all appends followed by the observed Reset are legal: %s", analysis.Reason)
	}
	if len(analysis.Missing) != int(resetID-1) {
		t.Fatalf("missing count = %d, want %d", len(analysis.Missing), resetID-1)
	}
}

func TestThirtyDistinctAppendsWithObservedRead(t *testing.T) {
	queueLengths := []int{8, 8, 7, 7}
	queues := make([]Queue, len(queueLengths))
	ids := make([][]RequestID, len(queueLengths))
	values := make([][]string, len(queueLengths))
	nextID := RequestID(1)
	for qi, length := range queueLengths {
		ids[qi] = make([]RequestID, length)
		values[qi] = make([]string, length)
		for idx := 0; idx < length; idx++ {
			id := nextID
			nextID++
			value := fmt.Sprintf("q%d-%d", qi, idx)
			ids[qi][idx] = id
			values[qi][idx] = value
			queues[qi].Commands = append(queues[qi].Commands, Command{
				ID:   id,
				Op:   OpAppend,
				Zone: 0,
				Data: Repeat([]byte(value), 1),
			})
		}
	}

	var expected Blocks
	expectedLBA := make(map[RequestID]int)
	position := 0
	for round := 0; ; round++ {
		progressed := false
		for qi := range queueLengths {
			if round >= queueLengths[qi] {
				continue
			}
			progressed = true
			expected = Join(expected, Repeat([]byte(values[qi][round]), 1))
			expectedLBA[ids[qi][round]] = position
			position++
		}
		if !progressed {
			break
		}
	}
	allAppends := make([]RequestID, 0, position)
	for qi := range queueLengths {
		allAppends = append(allAppends, ids[qi]...)
	}
	readID := nextID
	queues[0].Commands = append(queues[0].Commands, Command{
		ID:    readID,
		Op:    OpRead,
		Zone:  0,
		Addr:  0,
		Count: uint64(position),
		After: allAppends,
	})

	model := Model{
		Zones:  []Zone{{Capacity: uint64(position)}},
		Queues: queues,
	}
	obs := Observation{
		Zones: []ZoneObservation{{
			Zone:            0,
			HasWritePointer: true,
			WritePointer:    uint64(position),
		}},
		Receipts: []Receipt{{
			Request:   readID,
			HasBlocks: true,
			Blocks:    expected,
		}},
	}

	analysis, err := Analyze(model, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.Valid {
		t.Fatalf("the read pins the complete append order: %s", analysis.Reason)
	}
	for _, missing := range analysis.Missing {
		want := AppendOutcome(uint64(expectedLBA[missing.Request]))
		if len(missing.Possible) != 1 || !missing.Possible[0].Equal(want) {
			t.Fatalf("request %d: got %v, want only %v", missing.Request, missing.Possible, want)
		}
	}
}
