package netsec_test

import (
	"fmt"
	"sync"
	"testing"

	. "example.org/netsec/q0401"
)

func contractPermutations[T any](values []T) [][]T {
	current := append([]T(nil), values...)
	var out [][]T
	var generate func(int)
	generate = func(at int) {
		if at == len(current) {
			out = append(out, append([]T(nil), current...))
			return
		}
		for index := at; index < len(current); index++ {
			current[at], current[index] = current[index], current[at]
			generate(at + 1)
			current[at], current[index] = current[index], current[at]
		}
	}
	generate(0)
	return out
}

func contractEventIndex(events []Event, kind EventKind) int {
	for index, event := range events {
		if event.Kind == kind {
			return index
		}
	}
	return -1
}

func TestContractAllShortEventPermutations(t *testing.T) {
	kinds := []EventKind{Start, CancelRequest, CancelConfirm, ResultGenerated, ResultConsumed}
	all := contractPermutations(kinds)
	if len(all) != 120 {
		t.Fatalf("got %d permutations, want 120", len(all))
	}

	for permutationIndex, permutation := range all {
		events := make([]Event, len(permutation))
		for index, kind := range permutation {
			events[index] = Event{Kind: kind, OperationID: "op", Generation: 1}
		}

		requestIndex := contractEventIndex(events, CancelRequest)
		confirmIndex := contractEventIndex(events, CancelConfirm)
		consumeIndex := contractEventIndex(events, ResultConsumed)
		wantViolation := consumeIndex > confirmIndex

		report := Analyze(events)
		if report.Violated != wantViolation {
			t.Fatalf("permutation %d %v: Violated=%v, want %v",
				permutationIndex, permutation, report.Violated, wantViolation)
		}
		if len(report.Violations) != contractBoolInt(wantViolation) {
			t.Fatalf("permutation %d %v: got %d violations, want %d",
				permutationIndex, permutation, len(report.Violations), contractBoolInt(wantViolation))
		}

		if wantViolation {
			violation := report.Violations[0]
			if violation.Index != consumeIndex || violation.Position != consumeIndex+1 {
				t.Fatalf("permutation %d %v: violation position = (%d,%d), want (%d,%d)",
					permutationIndex, permutation, violation.Index, violation.Position,
					consumeIndex, consumeIndex+1)
			}
			if violation.ConfirmIndex != confirmIndex || violation.ConfirmPosition != confirmIndex+1 {
				t.Fatalf("permutation %d %v: confirm position = (%d,%d), want (%d,%d)",
					permutationIndex, permutation, violation.ConfirmIndex, violation.ConfirmPosition,
					confirmIndex, confirmIndex+1)
			}
			if violation.Generation != 1 || violation.EventKind != ResultConsumed {
				t.Fatalf("permutation %d %v: unexpected violation %+v",
					permutationIndex, permutation, violation)
			}
			if violation.Sequence != uint64(consumeIndex+1) {
				t.Fatalf("permutation %d %v: violation sequence = %d, want %d",
					permutationIndex, permutation, violation.Sequence, consumeIndex+1)
			}
		}

		if len(report.Consumptions) != 1 {
			t.Fatalf("permutation %d %v: got %d consumption records, want 1",
				permutationIndex, permutation, len(report.Consumptions))
		}
		consumption := report.Consumptions[0]
		switch {
		case consumeIndex > confirmIndex:
			if consumption.Order != ConsumptionAfterCancelConfirmation ||
				!consumption.AfterConfirmation || !consumption.Violation {
				t.Fatalf("permutation %d %v: bad forbidden order %+v",
					permutationIndex, permutation, consumption)
			}
		case consumeIndex > requestIndex:
			if consumption.Order != ConsumptionAfterCancelRequest ||
				!consumption.BeforeConfirmation || consumption.Violation {
				t.Fatalf("permutation %d %v: bad pre-confirm race %+v",
					permutationIndex, permutation, consumption)
			}
		default:
			if consumption.Order != ConsumptionBeforeCancelRequest ||
				!consumption.BeforeConfirmation || consumption.Violation {
				t.Fatalf("permutation %d %v: bad pre-request consumption %+v",
					permutationIndex, permutation, consumption)
			}
		}

		if len(report.Order) != len(events) {
			t.Fatalf("permutation %d %v: order has %d events, want %d",
				permutationIndex, permutation, len(report.Order), len(events))
		}
		for index, ordering := range report.Order {
			if ordering.Index != index || ordering.Position != index+1 ||
				ordering.Sequence != uint64(index+1) ||
				ordering.Event.Kind != events[index].Kind {
				t.Fatalf("permutation %d %v: bad ordering %+v at %d",
					permutationIndex, permutation, ordering, index)
			}
		}
	}
}

func TestContractGenerationReuseIsolation(t *testing.T) {
	events := []Event{
		{Kind: Start, OperationID: "scan", Generation: 1},
		{Kind: CancelRequest, OperationID: "scan", Generation: 1},
		{Kind: CancelConfirm, OperationID: "scan", Generation: 1},
		{Kind: ResultGenerated, OperationID: "scan", Generation: 1},
		{Kind: Start, OperationID: "scan", Generation: 2},
		{Kind: ResultGenerated, OperationID: "scan", Generation: 2},
		{Kind: ResultConsumed, OperationID: "scan", Generation: 2},
		{Kind: ResultConsumed, OperationID: "scan", Generation: 1},
	}

	report := Analyze(events)
	if len(report.Violations) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(report.Violations), report.Violations)
	}
	violation := report.Violations[0]
	if violation.Index != 7 || violation.Position != 8 ||
		violation.Generation != 1 || violation.ConfirmIndex != 2 {
		t.Fatalf("violation did not point at the old generation: %+v", violation)
	}

	if len(report.Generations) != 2 {
		t.Fatalf("got %d generations, want 2", len(report.Generations))
	}
	first, second := report.Generations[0], report.Generations[1]
	if first.Generation != 1 || !first.CancelConfirmed || first.ResultConsumed != 1 {
		t.Fatalf("bad first generation: %+v", first)
	}
	if second.Generation != 2 || second.CancelConfirmed || second.ResultConsumed != 1 {
		t.Fatalf("bad second generation: %+v", second)
	}
}

func TestContractCancellationOrderingRecords(t *testing.T) {
	tests := []struct {
		name       string
		events     []Event
		wantOrder  ConsumptionOrder
		wantBreach bool
	}{
		{
			name: "no cancellation",
			events: []Event{
				{Kind: Start, OperationID: "op", Generation: 1},
				{Kind: ResultConsumed, OperationID: "op", Generation: 1},
			},
			wantOrder: ConsumptionNoCancellation,
		},
		{
			name: "consume before cancellation request",
			events: []Event{
				{Kind: Start, OperationID: "op", Generation: 1},
				{Kind: ResultConsumed, OperationID: "op", Generation: 1},
				{Kind: CancelRequest, OperationID: "op", Generation: 1},
				{Kind: CancelConfirm, OperationID: "op", Generation: 1},
			},
			wantOrder: ConsumptionBeforeCancelRequest,
		},
		{
			name: "consume between request and confirmation",
			events: []Event{
				{Kind: Start, OperationID: "op", Generation: 1},
				{Kind: CancelRequest, OperationID: "op", Generation: 1},
				{Kind: ResultConsumed, OperationID: "op", Generation: 1},
				{Kind: CancelConfirm, OperationID: "op", Generation: 1},
			},
			wantOrder: ConsumptionAfterCancelRequest,
		},
		{
			name: "consume after confirmation",
			events: []Event{
				{Kind: Start, OperationID: "op", Generation: 1},
				{Kind: CancelRequest, OperationID: "op", Generation: 1},
				{Kind: CancelConfirm, OperationID: "op", Generation: 1},
				{Kind: ResultGenerated, OperationID: "op", Generation: 1},
				{Kind: ResultConsumed, OperationID: "op", Generation: 1},
			},
			wantOrder:  ConsumptionAfterCancelConfirmation,
			wantBreach: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := Analyze(test.events)
			if report.Violated != test.wantBreach {
				t.Fatalf("Violated=%v, want %v", report.Violated, test.wantBreach)
			}
			if len(report.Consumptions) != 1 {
				t.Fatalf("got %d consumption records, want 1", len(report.Consumptions))
			}
			if got := report.Consumptions[0].Order; got != test.wantOrder {
				t.Fatalf("Order=%q, want %q", got, test.wantOrder)
			}
		})
	}
}

func TestContractMultipleConsumptionsKeepExplicitOrder(t *testing.T) {
	events := []Event{
		{Kind: Start, OperationID: "op", Generation: 1},
		{Kind: CancelRequest, OperationID: "op", Generation: 1},
		{Kind: ResultConsumed, OperationID: "op", Generation: 1},
		{Kind: CancelConfirm, OperationID: "op", Generation: 1},
		{Kind: ResultConsumed, OperationID: "op", Generation: 1},
	}
	report := Analyze(events)

	if len(report.Consumptions) != 2 {
		t.Fatalf("got %d consumption records, want 2", len(report.Consumptions))
	}
	if got := report.Consumptions[0]; got.Index != 2 || got.Order != ConsumptionAfterCancelRequest || got.Violation {
		t.Fatalf("first consumption should be a legal race: %+v", got)
	}
	if got := report.Consumptions[1]; got.Index != 4 || got.Order != ConsumptionAfterCancelConfirmation || !got.Violation {
		t.Fatalf("second consumption should breach the barrier: %+v", got)
	}
	if len(report.Violations) != 1 || report.Violations[0].Index != 4 {
		t.Fatalf("unexpected violations: %+v", report.Violations)
	}
}

func TestContractAutoGenerationOnReuse(t *testing.T) {
	monitor := NewMonitor()
	monitor.Start("op")
	monitor.CancelRequest("op")
	monitor.CancelConfirm("op")

	if outcome := monitor.Start("op"); outcome.Generation != 1 {
		t.Fatalf("reused operation got generation %d, want 1", outcome.Generation)
	}
	if outcome := monitor.ResultGenerated("op"); outcome.Generation != 1 {
		t.Fatalf("latest generation should be 1, got %d", outcome.Generation)
	}
	if outcome := monitor.ResultConsumed("op"); outcome.Violation != nil {
		t.Fatalf("new generation was blocked by old cancellation: %+v", outcome.Violation)
	}

	report := monitor.Report()
	if report.Violated {
		t.Fatalf("unexpected violation: %+v", report.Violations)
	}
	if len(report.Generations) != 2 {
		t.Fatalf("got %d generations, want 2", len(report.Generations))
	}
	if !report.Generations[0].CancelConfirmed || report.Generations[1].CancelConfirmed {
		t.Fatalf("generation cancellation leaked: %+v", report.Generations)
	}
}

func TestContractConcurrentConfirmAndConsume(t *testing.T) {
	const iterations = 100
	for iteration := 0; iteration < iterations; iteration++ {
		operationID := fmt.Sprintf("op-%d", iteration)
		monitor := NewMonitor()
		monitor.Start(operationID, 1)
		monitor.CancelRequest(operationID, 1)

		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			monitor.CancelConfirm(operationID, 1)
		}()
		go func() {
			defer wait.Done()
			<-start
			monitor.ResultConsumed(operationID, 1)
		}()
		close(start)
		wait.Wait()

		report := monitor.Report()
		if len(report.Consumptions) != 1 {
			t.Fatalf("iteration %d: got %d consumptions, want 1", iteration, len(report.Consumptions))
		}

		var confirmSequence, consumeSequence uint64
		for _, ordering := range report.Order {
			switch ordering.Event.Kind {
			case CancelConfirm:
				confirmSequence = ordering.Sequence
			case ResultConsumed:
				consumeSequence = ordering.Sequence
			}
		}
		wantViolation := confirmSequence < consumeSequence
		if report.Violated != wantViolation {
			t.Fatalf("iteration %d: Violated=%v, confirm=%d consume=%d",
				iteration, report.Violated, confirmSequence, consumeSequence)
		}
		if len(report.Violations) != contractBoolInt(wantViolation) {
			t.Fatalf("iteration %d: got %d violations, want %d",
				iteration, len(report.Violations), contractBoolInt(wantViolation))
		}
		if report.Consumptions[0].Violation != wantViolation {
			t.Fatalf("iteration %d: consumption record disagrees: %+v",
				iteration, report.Consumptions[0])
		}
	}
}

func TestContractInputAliasesAndUnknownEvents(t *testing.T) {
	events := []Event{
		{Type: "started", Op: "op", Gen: 1},
		{Type: "cancel_requested", Op: "op", Gen: 1},
		{Type: "cancel_confirmed", Op: "op", Gen: 1},
		{Type: "result_produced", Op: "op", Gen: 1},
		{Type: "result_consumed", Op: "op", Gen: 1},
		{Type: "not-an-event", Op: "op", Gen: 1},
	}
	report := Analyze(events)
	if len(report.Violations) != 1 || report.Violations[0].Index != 4 {
		t.Fatalf("unexpected violations: %+v", report.Violations)
	}
	if len(report.Anomalies) != 1 || report.Anomalies[0].Kind != AnomalyUnknownEvent {
		t.Fatalf("unexpected anomalies: %+v", report.Anomalies)
	}
	if got := report.Events[0]; got.Kind != Start || got.OperationID != "op" || got.Generation != 1 {
		t.Fatalf("event was not canonicalized: %+v", got)
	}
}

func TestContractZeroValueAndReset(t *testing.T) {
	var monitor Monitor
	monitor.Start("op", 1)
	monitor.CancelRequest("op", 1)
	monitor.CancelConfirm("op", 1)
	monitor.ResultConsumed("op", 1)
	if monitor.OK() {
		t.Fatal("zero-value monitor did not report the violation")
	}
	if monitor.Len() != 4 {
		t.Fatalf("Len=%d, want 4", monitor.Len())
	}

	monitor.Reset()
	if monitor.Len() != 0 || !monitor.OK() {
		t.Fatalf("reset monitor is not empty: len=%d ok=%v", monitor.Len(), monitor.OK())
	}
	monitor.Start("op", 1)
	if monitor.Len() != 1 {
		t.Fatalf("monitor is not reusable after reset: len=%d", monitor.Len())
	}
}

func contractBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
