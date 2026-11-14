package netsec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type recordingAlertSink struct {
	mu     sync.Mutex
	alerts []Alert
	fail   int
}

func (s *recordingAlertSink) Send(_ context.Context, alert Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail > 0 {
		s.fail--
		return errors.New("alert transport unavailable")
	}
	s.alerts = append(s.alerts, alert)
	return nil
}

func (s *recordingAlertSink) snapshot() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Alert(nil), s.alerts...)
}

func successfulSystem(kind SystemKind) System {
	return System{
		ID:   "sys-" + string(kind),
		Kind: kind,
		Handler: CopyHandlerFunc(func(_ context.Context, task CopyTask) (Receipt, error) {
			receipt := task.Receipt()
			receipt.RecordsAffected = 2
			receipt.Evidence = "processed by " + task.SystemID
			return receipt, nil
		}),
	}
}

func standardSystems(overrides map[SystemKind]CopyHandler) []System {
	systems := make([]System, 0, len(StandardSystemKinds()))
	for _, kind := range StandardSystemKinds() {
		if handler := overrides[kind]; handler != nil {
			systems = append(systems, System{
				ID:      "sys-" + string(kind),
				Kind:    kind,
				Handler: handler,
			})
			continue
		}
		systems = append(systems, successfulSystem(kind))
	}
	return systems
}

func enrollAlice(t *testing.T, verifier *MemoryIdentityVerifier) {
	t.Helper()
	if err := verifier.Enroll(Subject{ID: "alice"}, "alice-secret"); err != nil {
		t.Fatalf("enroll alice: %v", err)
	}
}

func aliceRegistration(now time.Time) Registration {
	return Registration{
		Subject:  Subject{ID: "alice"},
		Kind:     RequestExport,
		Scope:    Scope{Categories: []string{" profile ", "orders", "profile"}},
		Deadline: now.Add(time.Hour),
		Proof:    IdentityProof{SubjectID: "alice", Secret: "alice-secret"},
	}
}

func TestLegitimateRequestCompletesEverySystem(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)

	tracker, err := NewTracker(Config{
		Verifier: verifier,
		Systems:  standardSystems(nil),
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if request.ID == "" {
		t.Fatal("request ID is empty")
	}
	if got, want := request.Scope.Categories, []string{"profile", "orders"}; !equalStrings(got, want) {
		t.Fatalf("normalized scope = %v, want %v", got, want)
	}

	before, err := tracker.Status(request.ID)
	if err != nil {
		t.Fatalf("status before processing: %v", err)
	}
	if before.Complete {
		t.Fatal("request was complete before any system reported")
	}
	if got, want := len(before.MissingSystems), len(StandardSystemKinds()); got != want {
		t.Fatalf("missing systems before processing = %d, want %d", got, want)
	}
	if err := tracker.VerifyComplete(request.ID); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("VerifyComplete before processing = %v, want ErrIncomplete", err)
	}

	status, err := tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !status.Complete || status.State != RequestCompleted {
		t.Fatalf("status = %#v, want completed with no missing systems", status)
	}
	if len(status.MissingSystems) != 0 {
		t.Fatalf("missing systems = %v, want none", status.MissingSystems)
	}
	if status.Overdue {
		t.Fatal("on-time request marked overdue")
	}
	if status.CompletedAt.IsZero() {
		t.Fatal("completion time is zero")
	}
	if len(status.Items) != len(StandardSystemKinds()) {
		t.Fatalf("items = %d, want %d", len(status.Items), len(StandardSystemKinds()))
	}
	for i, item := range status.Items {
		if item.State != ItemCompleted {
			t.Errorf("item %s state = %s, want completed", item.SystemID, item.State)
		}
		if item.Attempts != 1 {
			t.Errorf("item %s attempts = %d, want 1", item.SystemID, item.Attempts)
		}
		if item.Receipt == nil {
			t.Fatalf("item %s has no receipt", item.SystemID)
		}
		if item.SystemKind != StandardSystemKinds()[i] {
			t.Errorf("item order %d kind = %s", i, item.SystemKind)
		}
		assertReceipt(t, *item.Receipt, request, item.SystemID)
	}

	receipts, err := tracker.Receipts(request.ID)
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if len(receipts) != len(StandardSystemKinds()) {
		t.Fatalf("receipts = %d, want %d", len(receipts), len(StandardSystemKinds()))
	}
	for _, receipt := range receipts {
		assertReceipt(t, receipt, request, receipt.SystemID)
	}
	if err := tracker.VerifyComplete(request.ID); err != nil {
		t.Fatalf("VerifyComplete after all systems reported: %v", err)
	}
}

func TestCopyOmissionBlocksCompletion(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)

	backupFailure := errors.New("backup is offline")
	handlers := map[SystemKind]CopyHandler{
		SystemBackup: CopyHandlerFunc(func(_ context.Context, _ CopyTask) (Receipt, error) {
			return Receipt{}, backupFailure
		}),
	}
	tracker, err := NewTracker(Config{
		Verifier:    verifier,
		Systems:     standardSystems(handlers),
		MaxAttempts: 2,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	status, err := tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	if status.Complete {
		t.Fatal("request was reported complete while the backup had no receipt")
	}
	if status.State != RequestBlocked {
		t.Fatalf("state = %s, want blocked", status.State)
	}
	if got, want := status.MissingSystems, []string{"sys-backup"}; !equalStrings(got, want) {
		t.Fatalf("missing systems = %v, want %v", got, want)
	}

	backup := findItem(t, status, "sys-backup")
	if backup.State != ItemFailed {
		t.Fatalf("backup state = %s, want failed", backup.State)
	}
	if backup.Attempts != 2 {
		t.Fatalf("backup attempts = %d, want 2", backup.Attempts)
	}
	if backup.Receipt != nil {
		t.Fatal("failed backup unexpectedly has a receipt")
	}

	receipts, err := tracker.Receipts(request.ID)
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if got, want := len(receipts), len(StandardSystemKinds())-1; got != want {
		t.Fatalf("receipts = %d, want %d", got, want)
	}

	err = tracker.VerifyComplete(request.ID)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("VerifyComplete = %v, want ErrIncomplete", err)
	}
	var incomplete *IncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error type = %T, want *IncompleteError", err)
	}
	if !equalStrings(incomplete.Missing, []string{"sys-backup"}) {
		t.Fatalf("incomplete missing = %v", incomplete.Missing)
	}
	if !equalStrings(incomplete.Blocked, []string{"sys-backup"}) {
		t.Fatalf("incomplete blocked = %v", incomplete.Blocked)
	}
}

func TestImpersonatedRequestIsRejectedBeforeRegistration(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)
	if err := verifier.Enroll(Subject{ID: "bob"}, "bob-secret"); err != nil {
		t.Fatalf("enroll bob: %v", err)
	}

	tracker, err := NewTracker(Config{
		Verifier: verifier,
		Systems:  standardSystems(nil),
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	tests := []struct {
		name  string
		proof IdentityProof
	}{
		{
			name:  "different subject and secret",
			proof: IdentityProof{SubjectID: "bob", Secret: "bob-secret"},
		},
		{
			name:  "correct subject with another subject's secret",
			proof: IdentityProof{SubjectID: "alice", Secret: "bob-secret"},
		},
		{
			name:  "empty secret",
			proof: IdentityProof{SubjectID: "alice"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registration := aliceRegistration(clock.Now())
			registration.Proof = test.proof
			_, err := tracker.Register(context.Background(), registration)
			if !errors.Is(err, ErrIdentityVerificationFailed) {
				t.Fatalf("register error = %v, want ErrIdentityVerificationFailed", err)
			}
			if got := tracker.RequestCount(); got != 0 {
				t.Fatalf("request count = %d, want 0 after rejected identity", got)
			}
		})
	}

	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("legitimate register: %v", err)
	}
	if got := tracker.RequestCount(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
	if _, err := tracker.Process(context.Background(), request.ID); err != nil {
		t.Fatalf("process legitimate request: %v", err)
	}
	if err := tracker.VerifyComplete(request.ID); err != nil {
		t.Fatalf("legitimate request incomplete: %v", err)
	}
}

func TestTransientFailureIsRetried(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)

	var mu sync.Mutex
	attempts := 0
	handler := CopyHandlerFunc(func(_ context.Context, task CopyTask) (Receipt, error) {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()
		if current < 3 {
			return Receipt{}, fmt.Errorf("temporary error %d", current)
		}
		receipt := task.Receipt()
		receipt.Evidence = "recovered on attempt 3"
		return receipt, nil
	})

	tracker, err := NewTracker(Config{
		Verifier:    verifier,
		Systems:     standardSystems(map[SystemKind]CopyHandler{SystemCache: handler}),
		MaxAttempts: 3,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	status, err := tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !status.Complete {
		t.Fatalf("request incomplete after retry: %v", status.MissingSystems)
	}
	cache := findItem(t, status, "sys-cache")
	if cache.Attempts != 3 {
		t.Fatalf("cache attempts = %d, want 3", cache.Attempts)
	}
	if cache.Receipt == nil || cache.Receipt.Evidence != "recovered on attempt 3" {
		t.Fatalf("cache receipt = %#v", cache.Receipt)
	}
}

func TestOverdueAlertAndLateCompletion(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)
	sink := &recordingAlertSink{}

	var mu sync.Mutex
	backupHealthy := false
	backup := CopyHandlerFunc(func(_ context.Context, task CopyTask) (Receipt, error) {
		mu.Lock()
		healthy := backupHealthy
		mu.Unlock()
		if !healthy {
			return Receipt{}, errors.New("backup maintenance in progress")
		}
		receipt := task.Receipt()
		receipt.Evidence = "backup processed after maintenance"
		return receipt, nil
	})

	tracker, err := NewTracker(Config{
		Verifier:     verifier,
		Systems:      standardSystems(map[SystemKind]CopyHandler{SystemBackup: backup}),
		AlertSink:    sink,
		MaxAttempts:  2,
		RetryBackoff: time.Hour,
		Now:          clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	status, err := tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("initial process: %v", err)
	}
	if status.Complete {
		t.Fatal("request completed despite failed backup")
	}
	backupItem := findItem(t, status, "sys-backup")
	if backupItem.State != ItemRetrying {
		t.Fatalf("backup state = %s, want retrying", backupItem.State)
	}
	if backupItem.Attempts != 1 {
		t.Fatalf("backup attempts = %d, want 1", backupItem.Attempts)
	}
	if !backupItem.NextAttemptAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("next attempt = %s, want %s", backupItem.NextAttemptAt, clock.Now().Add(time.Hour))
	}
	if len(sink.snapshot()) != 0 {
		t.Fatal("alert sent before the deadline")
	}

	clock.Advance(2 * time.Hour)
	raised, err := tracker.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(raised) != 1 {
		t.Fatalf("raised alerts = %d, want 1", len(raised))
	}
	alert := raised[0]
	if alert.Kind != AlertOverdue || alert.RequestID != request.ID || alert.SubjectID != "alice" {
		t.Fatalf("unexpected alert: %#v", alert)
	}
	if got, want := alert.MissingSystems, []string{"sys-backup"}; !equalStrings(got, want) {
		t.Fatalf("alert missing = %v, want %v", got, want)
	}
	if alert.OverdueBy != time.Hour {
		t.Fatalf("overdue by = %s, want 1h", alert.OverdueBy)
	}
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("delivered alerts = %d, want 1", got)
	}

	raised, err = tracker.Tick(context.Background())
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(raised) != 0 {
		t.Fatalf("second tick raised %d duplicate alerts", len(raised))
	}
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("duplicate alerts delivered: %d", got)
	}

	mu.Lock()
	backupHealthy = true
	mu.Unlock()
	status, err = tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("process after maintenance: %v", err)
	}
	if !status.Complete {
		t.Fatalf("request incomplete after backup recovered: %v", status.MissingSystems)
	}
	if !status.Overdue {
		t.Fatal("late completion was not marked overdue")
	}
	if status.OverdueBy != time.Hour {
		t.Fatalf("late completion overdue by = %s, want 1h", status.OverdueBy)
	}
	if status.OverdueAlertedAt.IsZero() {
		t.Fatal("overdue alert timestamp is zero")
	}
	if err := tracker.VerifyComplete(request.ID); err != nil {
		t.Fatalf("VerifyComplete after late completion: %v", err)
	}
}

func TestReceiptMustMatchTaskAndSubject(t *testing.T) {
	t.Run("wrong system receipt is retried", func(t *testing.T) {
		clock := newFakeClock()
		verifier := NewMemoryIdentityVerifier()
		enrollAlice(t, verifier)

		var mu sync.Mutex
		attempts := 0
		index := CopyHandlerFunc(func(_ context.Context, task CopyTask) (Receipt, error) {
			mu.Lock()
			attempts++
			current := attempts
			mu.Unlock()
			if current == 1 {
				forged := task.Receipt()
				forged.SystemID = "sys-cache"
				return forged, nil
			}
			receipt := task.Receipt()
			receipt.Evidence = "valid receipt"
			return receipt, nil
		})

		tracker, err := NewTracker(Config{
			Verifier:    verifier,
			Systems:     standardSystems(map[SystemKind]CopyHandler{SystemSearchIndex: index}),
			MaxAttempts: 2,
			Now:         clock.Now,
		})
		if err != nil {
			t.Fatalf("new tracker: %v", err)
		}
		request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		status, err := tracker.Process(context.Background(), request.ID)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !status.Complete {
			t.Fatalf("request incomplete: %v", status.MissingSystems)
		}
		indexItem := findItem(t, status, "sys-search_index")
		if indexItem.Attempts != 2 {
			t.Fatalf("index attempts = %d, want 2", indexItem.Attempts)
		}
		if indexItem.Receipt == nil || indexItem.Receipt.Evidence != "valid receipt" {
			t.Fatalf("unexpected index receipt: %#v", indexItem.Receipt)
		}
	})

	t.Run("wrong subject receipt never completes", func(t *testing.T) {
		clock := newFakeClock()
		verifier := NewMemoryIdentityVerifier()
		enrollAlice(t, verifier)

		index := CopyHandlerFunc(func(_ context.Context, task CopyTask) (Receipt, error) {
			forged := task.Receipt()
			forged.SubjectID = "bob"
			return forged, nil
		})
		tracker, err := NewTracker(Config{
			Verifier:    verifier,
			Systems:     standardSystems(map[SystemKind]CopyHandler{SystemSearchIndex: index}),
			MaxAttempts: 1,
			Now:         clock.Now,
		})
		if err != nil {
			t.Fatalf("new tracker: %v", err)
		}
		request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		status, err := tracker.Process(context.Background(), request.ID)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if status.Complete {
			t.Fatal("request completed with a receipt for the wrong subject")
		}
		indexItem := findItem(t, status, "sys-search_index")
		if indexItem.State != ItemFailed {
			t.Fatalf("index state = %s, want failed", indexItem.State)
		}
		if !strings.Contains(indexItem.LastError, ErrInvalidReceipt.Error()) {
			t.Fatalf("last error = %q, want invalid receipt", indexItem.LastError)
		}
		if err := tracker.VerifyComplete(request.ID); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("VerifyComplete = %v, want ErrIncomplete", err)
		}
	})
}

func TestAlertDeliveryRetriesAfterSinkFailure(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)
	sink := &recordingAlertSink{fail: 1}

	backup := CopyHandlerFunc(func(_ context.Context, _ CopyTask) (Receipt, error) {
		return Receipt{}, errors.New("backup unavailable")
	})
	tracker, err := NewTracker(Config{
		Verifier:    verifier,
		Systems:     standardSystems(map[SystemKind]CopyHandler{SystemBackup: backup}),
		AlertSink:   sink,
		MaxAttempts: 1,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}
	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := tracker.Process(context.Background(), request.ID); err != nil {
		t.Fatalf("process: %v", err)
	}

	clock.Advance(time.Hour + time.Minute)
	raised, err := tracker.Tick(context.Background())
	if err == nil {
		t.Fatal("first tick succeeded despite sink failure")
	}
	if len(raised) != 1 {
		t.Fatalf("raised alerts = %d, want 1", len(raised))
	}
	records, err := tracker.Alerts(request.ID)
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	if len(records) != 1 || !records[0].DeliveredAt.IsZero() || records[0].LastError == "" {
		t.Fatalf("alert record after failed delivery = %#v", records)
	}

	raised, err = tracker.Tick(context.Background())
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(raised) != 0 {
		t.Fatalf("second tick raised %d alerts, want 0", len(raised))
	}
	records, err = tracker.Alerts(request.ID)
	if err != nil {
		t.Fatalf("alerts after delivery: %v", err)
	}
	if records[0].DeliveredAt.IsZero() || records[0].LastError != "" {
		t.Fatalf("alert record after successful delivery = %#v", records[0])
	}
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("sink alerts = %d, want 1", got)
	}
}

func TestProcessRaisesOverdueAlertWithoutTick(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)
	sink := &recordingAlertSink{}

	backup := CopyHandlerFunc(func(_ context.Context, _ CopyTask) (Receipt, error) {
		return Receipt{}, errors.New("backup unavailable")
	})
	tracker, err := NewTracker(Config{
		Verifier:    verifier,
		Systems:     standardSystems(map[SystemKind]CopyHandler{SystemBackup: backup}),
		AlertSink:   sink,
		MaxAttempts: 1,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}
	request, err := tracker.Register(context.Background(), aliceRegistration(clock.Now()))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	clock.Advance(time.Hour + time.Minute)
	status, err := tracker.Process(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !status.Overdue {
		t.Fatal("request was not marked overdue")
	}
	if !status.OverdueAlertedAt.Equal(clock.Now()) {
		t.Fatalf("alert time = %s, want %s", status.OverdueAlertedAt, clock.Now())
	}
	alerts := sink.snapshot()
	if len(alerts) != 1 || alerts[0].RequestID != request.ID {
		t.Fatalf("delivered alerts = %#v", alerts)
	}
}

func TestRegistrationValidation(t *testing.T) {
	clock := newFakeClock()
	verifier := NewMemoryIdentityVerifier()
	enrollAlice(t, verifier)
	tracker, err := NewTracker(Config{
		Verifier: verifier,
		Systems:  standardSystems(nil),
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatalf("new tracker: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Registration)
	}{
		{name: "empty subject", edit: func(r *Registration) { r.Subject.ID = " " }},
		{name: "unknown action", edit: func(r *Registration) { r.Kind = "archive" }},
		{name: "missing deadline", edit: func(r *Registration) { r.Deadline = time.Time{} }},
		{name: "empty category", edit: func(r *Registration) { r.Scope.Categories = []string{"profile", " "} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registration := aliceRegistration(clock.Now())
			test.edit(&registration)
			if _, err := tracker.Register(context.Background(), registration); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("register error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	if _, err := NewTracker(Config{Verifier: verifier}); !errors.Is(err, ErrNoCopySystems) {
		t.Fatalf("empty systems error = %v, want ErrNoCopySystems", err)
	}
	if _, err := NewTracker(Config{Systems: standardSystems(nil)}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("missing verifier error = %v, want ErrInvalidConfiguration", err)
	}
	primaryOnly := []System{successfulSystem(SystemPrimary)}
	if _, err := NewTracker(Config{Verifier: verifier, Systems: primaryOnly}); !errors.Is(err, ErrIncompleteInventory) {
		t.Fatalf("primary-only inventory error = %v, want ErrIncompleteInventory", err)
	}
}

func assertReceipt(t *testing.T, receipt Receipt, request Request, systemID string) {
	t.Helper()
	if receipt.RequestID != request.ID {
		t.Errorf("receipt request ID = %q, want %q", receipt.RequestID, request.ID)
	}
	if receipt.SubjectID != request.Subject.ID {
		t.Errorf("receipt subject ID = %q, want %q", receipt.SubjectID, request.Subject.ID)
	}
	if receipt.SystemID != systemID {
		t.Errorf("receipt system ID = %q, want %q", receipt.SystemID, systemID)
	}
	if receipt.Action != request.Kind {
		t.Errorf("receipt action = %q, want %q", receipt.Action, request.Kind)
	}
	if receipt.CompletedAt.IsZero() {
		t.Error("receipt completion time is zero")
	}
	if receipt.RecordsAffected < 0 {
		t.Errorf("receipt records affected = %d", receipt.RecordsAffected)
	}
}

func findItem(t *testing.T, status RequestStatus, systemID string) ItemStatus {
	t.Helper()
	for _, item := range status.Items {
		if item.SystemID == systemID {
			return item
		}
	}
	t.Fatalf("system %q not found in status", systemID)
	return ItemStatus{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
