package netsec

import (
	"testing"
)

func TestTimeWindowBoundary(t *testing.T) {
	local := TimeState{EngineBoots: 7, EngineTime: 1000}

	tests := []struct {
		name    string
		message TimeState
		want    Report
	}{
		{name: "149 seconds earlier", message: TimeState{EngineBoots: 7, EngineTime: 851}, want: ReportOK},
		{name: "149 seconds later", message: TimeState{EngineBoots: 7, EngineTime: 1149}, want: ReportOK},
		{name: "150 seconds earlier", message: TimeState{EngineBoots: 7, EngineTime: 850}, want: ReportOK},
		{name: "150 seconds later", message: TimeState{EngineBoots: 7, EngineTime: 1150}, want: ReportOK},
		{name: "151 seconds earlier", message: TimeState{EngineBoots: 7, EngineTime: 849}, want: NotInTimeWindow},
		{name: "151 seconds later", message: TimeState{EngineBoots: 7, EngineTime: 1151}, want: NotInTimeWindow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckTimeWindow(local, tt.message); got != tt.want {
				t.Fatalf("CheckTimeWindow() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidatorTimeWindowBoundary(t *testing.T) {
	v := NewValidator([]byte("local-engine"), 7, 1000, func(Message) bool { return true })
	base := Message{EngineID: []byte("local-engine"), Username: "alice", EngineBoots: 7}

	for _, delta := range []int64{-150, -149, 0, 149, 150} {
		msg := base
		msg.EngineTime = 1000 + delta
		if got := v.Process(msg); got != ReportOK {
			t.Fatalf("delta %d: Process() = %q, want OK", delta, got)
		}
	}

	for _, delta := range []int64{-151, 151} {
		msg := base
		msg.EngineTime = 1000 + delta
		if got := v.Process(msg); got != NotInTimeWindow {
			t.Fatalf("delta %d: Process() = %q, want %q", delta, got, NotInTimeWindow)
		}
	}
}

func TestDifferentAndReplayedEngineBootsAreRejected(t *testing.T) {
	v := NewValidator("local-engine", 7, 1000, func(Message) bool { return true })

	for _, boots := range []int32{6, 8} {
		msg := Message{
			EngineID:    []byte("local-engine"),
			Username:    "alice",
			EngineBoots: boots,
			EngineTime:  1000,
		}
		if got := v.Process(msg); got != NotInTimeWindow {
			t.Fatalf("engineBoots %d: Process() = %q, want %q", boots, got, NotInTimeWindow)
		}
	}
}

func TestUnauthenticatedMessageDoesNotUpdateSynchronizedState(t *testing.T) {
	v := NewValidator([]byte("local-engine"), 7, 1000, func(Message) bool { return false })
	before := v.SyncState()

	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: 7,
		EngineTime:  1010,
	}
	if got := v.Process(msg); got != AuthenticationFailed {
		t.Fatalf("Process() = %q, want %q", got, AuthenticationFailed)
	}
	if got := v.SyncState(); got != before {
		t.Fatalf("SyncState() changed before authentication: got %+v, want %+v", got, before)
	}
}

func TestAuthenticatedRejectedMessageDoesNotUpdateSynchronizedState(t *testing.T) {
	v := NewValidator("local-engine", 7, 1000, func(Message) bool { return true })
	before := v.SyncState()

	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: 7,
		EngineTime:  1151,
	}
	if got := v.Process(msg); got != NotInTimeWindow {
		t.Fatalf("Process() = %q, want %q", got, NotInTimeWindow)
	}
	if got := v.SyncState(); got != before {
		t.Fatalf("SyncState() changed after rejected message: got %+v, want %+v", got, before)
	}
}

func TestAuthenticatedAcceptedMessageUpdatesSynchronizedState(t *testing.T) {
	v := NewValidator("local-engine", 7, 1000, func(Message) bool { return true })

	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: 7,
		EngineTime:  1100,
	}
	if got := v.Process(msg); got != ReportOK {
		t.Fatalf("Process() = %q, want OK", got)
	}
	want := TimeState{EngineBoots: 7, EngineTime: 1100}
	if got := v.SyncState(); got != want {
		t.Fatalf("SyncState() = %+v, want %+v", got, want)
	}
}

func TestUnknownEngineDiscoveryDoesNotRevealUser(t *testing.T) {
	authCalls := 0
	v := NewValidator("local-engine", 7, 1000, func(Message) bool {
		authCalls++
		return true
	})
	before := v.SyncState()

	for _, username := range []string{"", "alice", "definitely-not-a-user"} {
		msg := Message{
			EngineID:    []byte("other-engine"),
			Username:    username,
			EngineBoots: 7,
			EngineTime:  1000,
			Discovery:   true,
		}
		if got := v.Process(msg); got != UnknownEngineID {
			t.Fatalf("username %q: Process() = %q, want %q", username, got, UnknownEngineID)
		}
	}
	if authCalls != 0 {
		t.Fatalf("authentication callback called %d times for discovery", authCalls)
	}
	if got := v.SyncState(); got != before {
		t.Fatalf("SyncState() changed during discovery: got %+v, want %+v", got, before)
	}
}

func TestUnknownUserAndAuthenticationFailureAreIndistinguishable(t *testing.T) {
	// alice exists but supplies a bad credential; nobody exists for "ghost".
	// The callback deliberately gives the package only a boolean result so the
	// two cases cannot produce different reports.
	auth := func(msg Message) bool {
		switch msg.Username {
		case "alice":
			return false // existing user, bad credential
		default:
			return false // unknown user
		}
	}
	v := NewValidator("local-engine", 7, 1000, auth)

	known := v.Process(Message{
		EngineID: []byte("local-engine"), Username: "alice",
		EngineBoots: 7, EngineTime: 1000,
	})
	unknown := v.Process(Message{
		EngineID: []byte("local-engine"), Username: "ghost",
		EngineBoots: 7, EngineTime: 1000,
	})

	if known != unknown {
		t.Fatalf("known-user failure report %q differs from unknown-user report %q", known, unknown)
	}
	if known != AuthenticationFailed {
		t.Fatalf("authentication failure report = %q, want %q", known, AuthenticationFailed)
	}
}

func TestEngineBootsCeilingRejectsAuthenticatedMessages(t *testing.T) {
	v := NewValidator("local-engine", MaxEngineBoots, 1000, func(Message) bool { return true })
	before := v.SyncState()

	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: MaxEngineBoots,
		EngineTime:  1000,
	}
	if got := v.Process(msg); got != NotInTimeWindow {
		t.Fatalf("Process() = %q, want %q", got, NotInTimeWindow)
	}
	if got := v.SyncState(); got != before {
		t.Fatalf("SyncState() changed at engineBoots ceiling: got %+v, want %+v", got, before)
	}
}

func TestTimeWindowRejectsUnauthenticatedBeforeWindowCheck(t *testing.T) {
	local := TimeState{EngineBoots: 7, EngineTime: 1000}
	message := TimeState{EngineBoots: 7, EngineTime: 1151}

	if got := ValidateTimeWindow(local, message, false); got != AuthenticationFailed {
		t.Fatalf("ValidateTimeWindow() = %q, want %q", got, AuthenticationFailed)
	}
	if got := ValidateTimeWindow(local, message, true); got != NotInTimeWindow {
		t.Fatalf("ValidateTimeWindow() = %q, want %q", got, NotInTimeWindow)
	}
}

func TestProcessWithCallerSuppliedLocalTime(t *testing.T) {
	v := NewValidator("local-engine", 1, 1, func(Message) bool { return true })
	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: 9,
		EngineTime:  2000,
	}

	if got := v.ProcessWithLocal(TimeState{EngineBoots: 9, EngineTime: 2000}, msg); got != ReportOK {
		t.Fatalf("ProcessWithLocal() = %q, want OK", got)
	}
	if got := v.LocalState(); got != (TimeState{EngineBoots: 9, EngineTime: 2000}) {
		t.Fatalf("LocalState() = %+v, want caller-supplied values", got)
	}
}

func TestProcessAcceptsAuthenticatedResult(t *testing.T) {
	v := NewValidator("local-engine", 7, 1000, nil)
	msg := Message{
		EngineID:    []byte("local-engine"),
		Username:    "alice",
		EngineBoots: 7,
		EngineTime:  1000,
	}

	if got := v.Process(msg, false); got != AuthenticationFailed {
		t.Fatalf("Process(msg, false) = %q, want %q", got, AuthenticationFailed)
	}
	if got := v.Process(msg, true); got != ReportOK {
		t.Fatalf("Process(msg, true) = %q, want OK", got)
	}
}
