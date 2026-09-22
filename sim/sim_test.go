package sim

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestAtLeastOnceScenarios(t *testing.T) {
	tests := []struct {
		name             string
		fault            Fault
		commandAttempts  int
		eventPublishes   int
		eventAttempts    int
		eventAcks        int
		orderCommits     int
		shipmentCommits  int
		duplicateCommand bool
		duplicateEvent   bool
		eventLogContains string
	}{
		{
			name:             "no fault delivers once",
			commandAttempts:  1,
			eventPublishes:   1,
			eventAttempts:    1,
			eventAcks:        1,
			orderCommits:     1,
			shipmentCommits:  1,
			eventLogContains: "committed shipment",
		},
		{
			name:             "crash before order commit is redelivered",
			fault:            Fault{Point: FaultBeforeOrderCommit, MessageID: "cmd-1"},
			commandAttempts:  2,
			eventPublishes:   1,
			eventAttempts:    1,
			eventAcks:        1,
			orderCommits:     1,
			shipmentCommits:  1,
			eventLogContains: "crash before order commit",
		},
		{
			name:             "lost response after commit does not rerun business",
			fault:            Fault{Point: FaultAfterOrderAck, MessageID: "cmd-1"},
			commandAttempts:  2,
			eventPublishes:   1,
			eventAttempts:    1,
			eventAcks:        1,
			duplicateCommand: true,
			orderCommits:     1,
			shipmentCommits:  1,
			eventLogContains: "response-lost after commit",
		},
		{
			name:             "outbox sent before mark is resent and deduplicated",
			fault:            Fault{Point: FaultAfterOutboxSend, MessageID: "evt-cmd-1"},
			commandAttempts:  1,
			eventPublishes:   2,
			eventAttempts:    2,
			eventAcks:        2,
			duplicateEvent:   true,
			orderCommits:     1,
			shipmentCommits:  1,
			eventLogContains: "crash before sent-mark",
		},
		{
			name:             "downstream crash before commit is redelivered once",
			fault:            Fault{Point: FaultBeforeEventCommit, MessageID: "evt-cmd-1"},
			commandAttempts:  1,
			eventPublishes:   1,
			eventAttempts:    2,
			eventAcks:        1,
			orderCommits:     1,
			shipmentCommits:  1,
			eventLogContains: "crash before event commit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var faults []Fault
			if tt.fault.Point != "" {
				faults = append(faults, tt.fault)
			}
			report, err := RunScenario(ScenarioConfig{Name: tt.name, Faults: faults})
			if err != nil {
				t.Fatalf("RunScenario() error = %v", err)
			}
			if !report.OrderExists || !report.OutboxExists || !report.Outbox.Sent || !report.ShipmentExists {
				t.Fatalf("workflow did not settle: order=%v outbox=%v shipment=%v", report.OrderExists, report.Outbox, report.ShipmentExists)
			}
			if got := report.Bus.AttemptCount["cmd-1"]; got != tt.commandAttempts {
				t.Fatalf("command attempts = %d, want %d", got, tt.commandAttempts)
			}
			if got := report.Bus.PublishCount["evt-cmd-1"]; got != tt.eventPublishes {
				t.Fatalf("event publishes = %d, want %d", got, tt.eventPublishes)
			}
			if got := report.Bus.AttemptCount["evt-cmd-1"]; got != tt.eventAttempts {
				t.Fatalf("event attempts = %d, want %d", got, tt.eventAttempts)
			}
			if got := report.Bus.AckCount["evt-cmd-1"]; got != tt.eventAcks {
				t.Fatalf("event acks = %d, want %d", got, tt.eventAcks)
			}
			if got := countEvents(report.Events, "committed order="); got != tt.orderCommits {
				t.Fatalf("order business commits = %d, want %d", got, tt.orderCommits)
			}
			if got := countEvents(report.Events, "committed shipment order="); got != tt.shipmentCommits {
				t.Fatalf("shipment business commits = %d, want %d", got, tt.shipmentCommits)
			}
			if tt.duplicateCommand && !slices.ContainsFunc(report.Events, func(event string) bool {
				return strings.Contains(event, "skip duplicate command=cmd-1")
			}) {
				t.Fatalf("missing duplicate command skip in events: %v", report.Events)
			}
			if tt.duplicateEvent && !slices.ContainsFunc(report.Events, func(event string) bool {
				return strings.Contains(event, "skip duplicate event=evt-cmd-1")
			}) {
				t.Fatalf("missing duplicate event skip in events: %v", report.Events)
			}
			if !slices.ContainsFunc(report.Events, func(event string) bool {
				return strings.Contains(event, tt.eventLogContains)
			}) {
				t.Fatalf("event log does not contain %q: %v", tt.eventLogContains, report.Events)
			}
		})
	}
}

func TestUncommittedOrderTransactionHasNoLocalOrExternalEffect(t *testing.T) {
	events := make([]string, 0)
	scheduler := NewScheduler(0)
	bus := NewBus(scheduler, func(_ int64, event string) { events = append(events, event) }, 1, 1)
	store := NewStore()
	faults := NewFaultInjector(Fault{Point: FaultBeforeOrderCommit, MessageID: "cmd-x"})
	app := NewOrderApp("order-service", store, bus, scheduler, faults, func(_ int64, event string) { events = append(events, event) })

	err := app.safe(app.HandleCommand)(Message{ID: "cmd-x", Topic: TopicCommands, Payload: "order-x"})
	if !errors.Is(err, ErrCrash) {
		t.Fatalf("HandleCommand() error = %v, want %v", err, ErrCrash)
	}
	if exists, err := store.Begin().Has("inbox:cmd-x"); err != nil || exists {
		t.Fatalf("inbox after abort exists=%v err=%v, want false", exists, err)
	}
	if exists, err := store.Begin().Has("order:order-x"); err != nil || exists {
		t.Fatalf("order after abort exists=%v err=%v, want false", exists, err)
	}
	if exists, err := store.Begin().Has("outbox:evt-cmd-x"); err != nil || exists {
		t.Fatalf("outbox after abort exists=%v err=%v, want false", exists, err)
	}
	if got := bus.Report().PublishCount["evt-cmd-x"]; got != 0 {
		t.Fatalf("event publishes = %d, want 0", got)
	}
	if got := bus.Report().PublishCount["cmd-x"]; got != 0 {
		t.Fatalf("command test published via bus = %d, want 0", got)
	}
}

func TestUncommittedDownstreamTransactionHasNoPartialEffect(t *testing.T) {
	scheduler := NewScheduler(0)
	bus := NewBus(scheduler, func(int64, string) {}, 1, 1)
	store := NewStore()
	faults := NewFaultInjector(Fault{Point: FaultBeforeEventCommit, MessageID: "evt-y"})
	app := NewShipmentApp("shipment-service", store, bus, scheduler, faults, func(int64, string) {})

	err := app.safe(app.HandleEvent)(Message{ID: "evt-y", Topic: TopicOrderEvents, Payload: "order-y"})
	if !errors.Is(err, ErrCrash) {
		t.Fatalf("HandleEvent() error = %v, want %v", err, ErrCrash)
	}
	if exists, err := store.Begin().Has("inbox:evt-y"); err != nil || exists {
		t.Fatalf("inbox after abort exists=%v err=%v, want false", exists, err)
	}
	if exists, err := store.Begin().Has("shipment:order-y"); err != nil || exists {
		t.Fatalf("shipment after abort exists=%v err=%v, want false", exists, err)
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	config := ScenarioConfig{
		Name:   "replay",
		Faults: []Fault{{Point: FaultAfterOutboxSend, MessageID: "evt-cmd-1"}},
	}
	first, err := RunScenario(config)
	if err != nil {
		t.Fatalf("first RunScenario() error = %v", err)
	}
	second, err := RunScenario(config)
	if err != nil {
		t.Fatalf("second RunScenario() error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("replay is not deterministic\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

func TestStoreTransactionSnapshotIsolation(t *testing.T) {
	store := NewStore()
	first := store.Begin()
	if err := first.Put("order:1", OrderRecord{ID: "1", Status: "created"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}

	reader := store.Begin()
	writer := store.Begin()
	if err := writer.Put("order:1", OrderRecord{ID: "1", Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	var readBeforeCommit OrderRecord
	ok, err := reader.Get("order:1", &readBeforeCommit)
	if err != nil || !ok {
		t.Fatalf("reader Get() ok=%v err=%v", ok, err)
	}
	if readBeforeCommit.Status != "created" {
		t.Fatalf("reader saw uncommitted status %q", readBeforeCommit.Status)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	var readAfterCommit OrderRecord
	if _, err := store.Begin().Get("order:1", &readAfterCommit); err != nil {
		t.Fatal(err)
	}
	if readAfterCommit.Status != "cancelled" {
		t.Fatalf("committed status = %q, want cancelled", readAfterCommit.Status)
	}
}

func countEvents(events []string, fragment string) int {
	count := 0
	for _, event := range events {
		if strings.Contains(event, fragment) {
			count++
		}
	}
	return count
}
