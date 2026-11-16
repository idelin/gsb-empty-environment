package netsec_test

import (
	"errors"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0718"
)

var testEngineID = []byte{0x80, 0x00, 0x00, 0x01}

func testState() *netsec.State {
	return netsec.NewState(testEngineID, 10, 1000)
}

func testMessage(user string, boots, engineTime int32) netsec.Message {
	return netsec.Message{
		EngineID:    append([]byte(nil), testEngineID...),
		EngineBoots: boots,
		EngineTime:  engineTime,
		UserName:    user,
	}
}

func authenticate(ok bool) netsec.AuthenticateFunc {
	return func(netsec.Message) bool { return ok }
}

func TestTimeWindowBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		messageTime  int32
		wantAccepted bool
		wantReport   netsec.Report
	}{
		{
			name:         "149 seconds behind",
			messageTime:  851,
			wantAccepted: true,
		},
		{
			name:         "149 seconds ahead",
			messageTime:  1149,
			wantAccepted: true,
		},
		{
			name:         "150 seconds ahead",
			messageTime:  1150,
			wantAccepted: true,
		},
		{
			name:        "151 seconds ahead",
			messageTime: 1151,
			wantReport:  netsec.NotInTimeWindowReport,
		},
		{
			name:        "151 seconds behind",
			messageTime: 849,
			wantReport:  netsec.NotInTimeWindowReport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := testState()
			msg := testMessage("alice", local.EngineBoots, tt.messageTime)

			if got := netsec.InTimeWindow(local.EngineBoots, local.EngineTime, msg.EngineBoots, msg.EngineTime); got != tt.wantAccepted {
				t.Fatalf("InTimeWindow() = %v, want %v", got, tt.wantAccepted)
			}

			got := local.Check(msg, authenticate(true))
			if got.Accepted != tt.wantAccepted {
				t.Fatalf("Accepted = %v, want %v (report %v)", got.Accepted, tt.wantAccepted, got.Report)
			}
			if got.Report != tt.wantReport {
				t.Fatalf("Report = %#v, want %#v", got.Report, tt.wantReport)
			}
			if !tt.wantAccepted {
				assertState(t, local, testState())
			}
		})
	}
}

func TestReplayOldEngineBoots(t *testing.T) {
	local := testState()
	before := *local

	result := local.Check(testMessage("alice", local.EngineBoots-1, local.EngineTime), authenticate(true))
	if result.Accepted {
		t.Fatal("message with replayed engineBoots was accepted")
	}
	if result.Report != netsec.NotInTimeWindowReport {
		t.Fatalf("Report = %#v, want %#v", result.Report, netsec.NotInTimeWindowReport)
	}
	assertState(t, local, &before)

	result = local.Check(testMessage("alice", local.EngineBoots+1, local.EngineTime), authenticate(true))
	if result.Report != netsec.NotInTimeWindowReport {
		t.Fatalf("boots mismatch Report = %#v, want %#v", result.Report, netsec.NotInTimeWindowReport)
	}
	assertState(t, local, &before)
}

func TestAuthenticationPrecedesStateUpdate(t *testing.T) {
	local := testState()
	before := *local
	calls := 0

	result := local.Check(testMessage("alice", local.EngineBoots, local.EngineTime+1), func(netsec.Message) bool {
		calls++
		return false
	})

	if calls != 1 {
		t.Fatalf("authentication calls = %d, want 1", calls)
	}
	if result.Accepted {
		t.Fatal("message with failed authentication was accepted")
	}
	if result.Report != netsec.AuthenticationFailureReport {
		t.Fatalf("Report = %#v, want %#v", result.Report, netsec.AuthenticationFailureReport)
	}
	assertState(t, local, &before)
}

func TestUnknownUserAndAuthenticationFailureAreIndistinguishable(t *testing.T) {
	local := testState()
	before := *local

	knownUsers := netsec.UserExistsFunc(func(user string) bool {
		return user == "alice"
	})

	authCalls := 0
	badPassword := local.Check(
		testMessage("alice", local.EngineBoots, local.EngineTime),
		knownUsers,
		func(netsec.Message) bool {
			authCalls++
			return false
		},
	)
	unknownUser := local.Check(
		testMessage("mallory", local.EngineBoots, local.EngineTime),
		knownUsers,
		func(netsec.Message) bool {
			t.Fatal("authenticator called for a non-existent user")
			return true
		},
	)

	if authCalls != 1 {
		t.Fatalf("authentication calls = %d, want 1", authCalls)
	}
	if badPassword.Accepted || unknownUser.Accepted {
		t.Fatal("failed authentication was accepted")
	}
	if badPassword.Report != unknownUser.Report {
		t.Fatalf("reports differ: bad password %#v, unknown user %#v", badPassword.Report, unknownUser.Report)
	}
	if badPassword.Report.Code != netsec.ReportAuthenticationFailure || badPassword.Report.OID != netsec.OIDWrongDigest {
		t.Fatalf("unexpected authentication report: %#v", badPassword.Report)
	}
	assertState(t, local, &before)
}

func TestUnknownEngineDiscoveryDoesNotProbeUsers(t *testing.T) {
	local := testState()
	before := *local

	callbackCalls := 0
	discovery := testMessage("alice", 0, 0)
	discovery.EngineID = []byte{0x80, 0x00, 0x00, 0x02}
	discovery.Discovery = true

	knownUserRegistration := local.Check(discovery, func(netsec.Message) bool {
		callbackCalls++
		return true
	})
	unknownUserRegistration := local.Check(discovery, func(user string) bool {
		callbackCalls++
		return false
	}, func(netsec.Message) bool {
		callbackCalls++
		return true
	})

	if callbackCalls != 0 {
		t.Fatalf("callbacks invoked for unknown-engine discovery: %d", callbackCalls)
	}
	if knownUserRegistration.Report != netsec.UnknownEngineIDReport {
		t.Fatalf("Report = %#v, want %#v", knownUserRegistration.Report, netsec.UnknownEngineIDReport)
	}
	if knownUserRegistration.Report != unknownUserRegistration.Report {
		t.Fatalf("reports differ across users: %#v vs %#v", knownUserRegistration.Report, unknownUserRegistration.Report)
	}
	assertState(t, local, &before)
}

func TestEngineBootsMaximumRejectsAuthenticatedMessages(t *testing.T) {
	local := netsec.NewState(testEngineID, netsec.MaxEngineBoots, 1000)
	before := *local

	result := local.Check(testMessage("alice", netsec.MaxEngineBoots, 1000), authenticate(true))
	if result.Accepted {
		t.Fatal("authenticated message accepted when engineBoots is at maximum")
	}
	if result.Report != netsec.NotInTimeWindowReport {
		t.Fatalf("Report = %#v, want %#v", result.Report, netsec.NotInTimeWindowReport)
	}
	if netsec.InTimeWindow(netsec.MaxEngineBoots, 1000, netsec.MaxEngineBoots, 1000) {
		t.Fatal("time window accepted state at maximum engineBoots")
	}
	assertState(t, local, &before)
}

func TestAcceptedMessageAdvancesButNeverRollsBackTime(t *testing.T) {
	local := testState()

	ahead := local.Check(testMessage("alice", local.EngineBoots, local.EngineTime+100), authenticate(true))
	if !ahead.Accepted || !ahead.Synced {
		t.Fatalf("ahead message was not accepted and synced: %#v", ahead)
	}
	if local.EngineTime != 1100 {
		t.Fatalf("engineTime = %d, want 1100", local.EngineTime)
	}

	behind := local.Check(testMessage("alice", local.EngineBoots, local.EngineTime-149), authenticate(true))
	if !behind.Accepted {
		t.Fatalf("149-second-old message was rejected: %#v", behind)
	}
	if behind.Synced {
		t.Fatal("state moved backwards after an accepted message")
	}
	if local.EngineTime != 1100 {
		t.Fatalf("engineTime moved backwards to %d", local.EngineTime)
	}
}

func TestValueAndPointerStateForms(t *testing.T) {
	local := *testState()
	msg := testMessage("alice", local.EngineBoots, local.EngineTime+100)

	result := netsec.Process(msg, local, authenticate(true))
	if !result.Accepted || result.State.EngineTime != 1100 {
		t.Fatalf("value Process result = %#v", result)
	}
	if local.EngineTime != 1000 {
		t.Fatalf("value Process mutated caller state: %d", local.EngineTime)
	}

	pointer := testState()
	result = netsec.Evaluate(msg, pointer, authenticate(true))
	if !result.Accepted || result.State.EngineTime != 1100 {
		t.Fatalf("pointer Evaluate result = %#v", result)
	}
	if pointer.EngineTime != 1000 {
		t.Fatalf("Evaluate mutated caller state: %d", pointer.EngineTime)
	}
}

func TestResultErrors(t *testing.T) {
	result := netsec.ProcessMessage(testMessage("alice", 10, 1000), testState(), authenticate(false))
	if !errors.Is(result, netsec.ErrAuthenticationFailure) {
		t.Fatalf("errors.Is(%v, ErrAuthenticationFailure) = false", result.Err())
	}
}

func assertState(t *testing.T, got, want *netsec.State) {
	t.Helper()
	if !reflect.DeepEqual(*got, *want) {
		t.Fatalf("state = %#v, want %#v", *got, *want)
	}
}
