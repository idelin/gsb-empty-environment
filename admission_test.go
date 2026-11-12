package netsec

import (
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
}

func splitFragments(payload []byte, startSequence uint8, cuts ...int) []TransportFragment {
	if len(cuts) == 0 {
		return []TransportFragment{{
			FIR:      true,
			FIN:      true,
			Sequence: startSequence,
			Payload:  payload,
		}}
	}

	boundaries := make([]int, 0, len(cuts)+2)
	boundaries = append(boundaries, 0)
	boundaries = append(boundaries, cuts...)
	boundaries = append(boundaries, len(payload))

	fragments := make([]TransportFragment, 0, len(boundaries)-1)
	for i := 0; i < len(boundaries)-1; i++ {
		fragments = append(fragments, TransportFragment{
			FIR:      i == 0,
			FIN:      i == len(boundaries)-2,
			Sequence: (startSequence + uint8(i)) & transportSeqMask,
			Payload:  payload[boundaries[i]:boundaries[i+1]],
		})
	}
	return fragments
}

func applicationBytes(function FunctionCode, sequence uint8, objects ...byte) []byte {
	return append(EncodeApplicationRequest(function, sequence), objects...)
}

func requestFor(source, destination uint16, payload []byte, sequence uint8, cuts ...int) Request {
	return Request{
		Source:      source,
		Destination: destination,
		Fragments:   splitFragments(payload, sequence, cuts...),
	}
}

func TestFragmentedFunctionCodeIsCheckedAfterReassembly(t *testing.T) {
	clock := newFakeClock()
	gateway := New(Policy{
		MaintenanceMasters: []uint16{10},
		Clock:              clock.Now,
	})

	selectPayload := applicationBytes(FCSelect, 5, 0x01, 0x02, 0x03)
	selectRequest := requestFor(10, 20, selectPayload, 5, 1, 3)
	if got := gateway.Admit(selectRequest); !got.Allowed {
		t.Fatalf("fragmented SELECT was denied: %s", got.Reason)
	}
	if selectRequest.Fragments[0].FIR != true || selectRequest.Fragments[0].FIN != false {
		t.Fatal("test did not build a multi-fragment request")
	}
	if got := selectRequest.Fragments[0].Payload[0]; got != selectPayload[0] {
		t.Fatal("test did not split before the function code")
	}

	operatePayload := applicationBytes(FCOperate, 5, 0xaa)
	if got := gateway.Admit(requestFor(10, 20, operatePayload, 5, 2)); !got.Allowed {
		t.Fatalf("fragmented matching OPERATE was denied: %s", got.Reason)
	}
}

func TestReassemblyRejectsIncompleteAndMalformedFrames(t *testing.T) {
	valid := applicationBytes(FCRead, 1)

	tests := []struct {
		name       string
		fragments  []TransportFragment
		wantReason Reason
	}{
		{
			name:       "no fragments",
			fragments:  nil,
			wantReason: ReasonIncompleteAssembly,
		},
		{
			name: "missing FIN",
			fragments: []TransportFragment{{
				FIR: true, FIN: false, Sequence: 1, Payload: valid,
			}},
			wantReason: ReasonIncompleteAssembly,
		},
		{
			name: "missing FIR",
			fragments: []TransportFragment{{
				FIR: false, FIN: true, Sequence: 1, Payload: valid,
			}},
			wantReason: ReasonIncompleteAssembly,
		},
		{
			name: "FIN in first fragment with trailing data",
			fragments: []TransportFragment{
				{FIR: true, FIN: true, Sequence: 1, Payload: valid[:1]},
				{FIR: false, FIN: true, Sequence: 2, Payload: valid[1:]},
			},
			wantReason: ReasonMalformedTransport,
		},
		{
			name: "transport sequence gap",
			fragments: []TransportFragment{
				{FIR: true, FIN: false, Sequence: 1, Payload: valid[:1]},
				{FIR: false, FIN: true, Sequence: 3, Payload: valid[1:]},
			},
			wantReason: ReasonMalformedTransport,
		},
		{
			name: "second message inside request",
			fragments: []TransportFragment{
				{FIR: true, FIN: false, Sequence: 1, Payload: valid[:1]},
				{FIR: true, FIN: true, Sequence: 2, Payload: valid[1:]},
			},
			wantReason: ReasonMalformedTransport,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := New(Policy{Clock: newFakeClock().Now})
			decision := gateway.Admit(Request{
				Source:      10,
				Destination: 20,
				Fragments:   test.fragments,
			})
			if decision.Allowed {
				t.Fatal("malformed or incomplete request was admitted")
			}
			if decision.Reason != test.wantReason {
				t.Fatalf("reason = %s, want %s", decision.Reason, test.wantReason)
			}
		})
	}
}

func TestDefaultMaintenanceAuthorization(t *testing.T) {
	gateway := New(Policy{Clock: newFakeClock().Now})

	for _, function := range []FunctionCode{FCColdRestart, FCHotRestart} {
		if got := gateway.Admit(requestFor(DefaultMaintenanceMaster, 20, applicationBytes(function, 1), 1)); !got.Allowed {
			t.Fatalf("%s from default maintenance master denied: %s", function, got.Reason)
		}
		if got := gateway.Admit(requestFor(7, 20, applicationBytes(function, 2), 2)); got.Allowed {
			t.Fatalf("%s from ordinary master was admitted", function)
		}
	}
}

func TestColdAndHotRestartAuthorizationsAreIndependent(t *testing.T) {
	gateway := New(Policy{
		MaintenanceMasters: []uint16{42},
		ColdRestartMasters: []uint16{7},
		HotRestartMasters:  []uint16{8},
		Clock:              newFakeClock().Now,
	})

	tests := []struct {
		name     string
		source   uint16
		function FunctionCode
		allowed  bool
	}{
		{name: "cold master may cold restart", source: 7, function: FCColdRestart, allowed: true},
		{name: "cold master may not hot restart", source: 7, function: FCHotRestart, allowed: false},
		{name: "hot master may hot restart", source: 8, function: FCHotRestart, allowed: true},
		{name: "hot master may not cold restart", source: 8, function: FCColdRestart, allowed: false},
		{name: "maintenance does not override dedicated cold list", source: 42, function: FCColdRestart, allowed: false},
		{name: "maintenance does not override dedicated hot list", source: 42, function: FCHotRestart, allowed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := applicationBytes(test.function, 9)
			decision := gateway.Admit(requestFor(test.source, 20, payload, 9, 1))
			if decision.Allowed != test.allowed {
				t.Fatalf("allowed = %v, want %v (reason %s)", decision.Allowed, test.allowed, decision.Reason)
			}
			if !test.allowed && decision.Reason != ReasonRestartNotAuthorized {
				t.Fatalf("reason = %s, want %s", decision.Reason, ReasonRestartNotAuthorized)
			}
		})
	}
}

func TestUnknownFunctionCodesAreAlwaysRejected(t *testing.T) {
	gateway := New(Policy{
		// Even an explicit allow-list entry for an unknown code must not make
		// it known.
		AllowedFunctionCodes: []FunctionCode{FunctionCode(0x7f), FCRead},
		Clock:                newFakeClock().Now,
	})

	unknown := gateway.Admit(requestFor(10, 20, applicationBytes(FunctionCode(0x7f), 1), 1))
	if unknown.Allowed || unknown.Reason != ReasonUnknownFunction {
		t.Fatalf("unknown function decision = %v/%s", unknown.Allowed, unknown.Reason)
	}

	knownButDenied := gateway.Admit(requestFor(10, 20, applicationBytes(FCWrite, 2), 2))
	if knownButDenied.Allowed || knownButDenied.Reason != ReasonFunctionNotAllowed {
		t.Fatalf("disallowed known function decision = %v/%s", knownButDenied.Allowed, knownButDenied.Reason)
	}

	allowed := gateway.Admit(requestFor(10, 20, applicationBytes(FCRead, 3), 3))
	if !allowed.Allowed {
		t.Fatalf("allow-listed function was denied: %s", allowed.Reason)
	}
}

func TestEmptyFunctionAllowListDeniesEverything(t *testing.T) {
	gateway := New(Policy{
		AllowedFunctionCodes: []FunctionCode{},
		Clock:                newFakeClock().Now,
	})
	decision := gateway.Admit(requestFor(10, 20, applicationBytes(FCRead, 1), 1))
	if decision.Allowed || decision.Reason != ReasonFunctionNotAllowed {
		t.Fatalf("empty allow-list decision = %v/%s", decision.Allowed, decision.Reason)
	}
}

func TestMalformedApplicationAndOversizedMessageRejected(t *testing.T) {
	gateway := New(Policy{
		MaxMessageBytes: 8,
		Clock:           newFakeClock().Now,
	})

	tests := []struct {
		name       string
		fragments  []TransportFragment
		wantReason Reason
	}{
		{
			name: "payload too short for application header",
			fragments: splitFragments([]byte{
				applicationFIRMask | applicationFINMask,
			}, 0),
			wantReason: ReasonMalformedApplication,
		},
		{
			name: "application fragment lacks FIN",
			fragments: splitFragments([]byte{
				applicationFIRMask,
				byte(FCRead),
			}, 0),
			wantReason: ReasonMalformedApplication,
		},
		{
			name: "application fragment lacks FIR",
			fragments: splitFragments([]byte{
				applicationFINMask,
				byte(FCRead),
			}, 0),
			wantReason: ReasonMalformedApplication,
		},
		{
			name:       "assembled payload exceeds policy limit",
			fragments:  splitFragments(make([]byte, 9), 0),
			wantReason: ReasonMalformedTransport,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := gateway.Admit(Request{
				Source:      10,
				Destination: 20,
				Fragments:   test.fragments,
			})
			if decision.Allowed || decision.Reason != test.wantReason {
				t.Fatalf("decision = %v/%s, want denial %s", decision.Allowed, decision.Reason, test.wantReason)
			}
		})
	}
}

func TestSelectAndOperateMustMatchMasterDestinationAndSequence(t *testing.T) {
	gateway := New(Policy{Clock: newFakeClock().Now})

	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCSelect, 5), 5)); !got.Allowed {
		t.Fatalf("SELECT denied: %s", got.Reason)
	}

	tests := []struct {
		name       string
		request    Request
		wantReason Reason
	}{
		{
			name:       "different master",
			request:    requestFor(11, 20, applicationBytes(FCOperate, 5), 5),
			wantReason: ReasonSelectMasterMismatch,
		},
		{
			name:       "different sequence",
			request:    requestFor(10, 20, applicationBytes(FCOperate, 6), 6),
			wantReason: ReasonSelectSequenceMismatch,
		},
		{
			name:       "different destination",
			request:    requestFor(10, 21, applicationBytes(FCOperate, 5), 5),
			wantReason: ReasonSelectDestinationMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := gateway.Admit(test.request)
			if decision.Allowed {
				t.Fatal("mismatched OPERATE was admitted")
			}
			if decision.Reason != test.wantReason {
				t.Fatalf("reason = %s, want %s", decision.Reason, test.wantReason)
			}
		})
	}

	// The failed attempts above must not consume the valid SELECT.
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 5), 5, 2)); !got.Allowed {
		t.Fatalf("matching OPERATE denied after mismatched attempts: %s", got.Reason)
	}
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 5), 5)); got.Allowed {
		t.Fatal("replayed OPERATE was admitted")
	}
}

func TestSelectOperateTimeoutUsesCallerLimit(t *testing.T) {
	clock := newFakeClock()
	gateway := New(Policy{
		SelectTimeout: time.Second,
		Clock:         clock.Now,
	})

	selectPayload := applicationBytes(FCSelect, 3)
	selectRequest := requestFor(10, 20, selectPayload, 3)
	selectRequest.Timeout = 200 * time.Millisecond
	if got := gateway.Admit(selectRequest); !got.Allowed {
		t.Fatalf("SELECT denied: %s", got.Reason)
	}

	clock.Advance(199 * time.Millisecond)
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 3), 3)); !got.Allowed {
		t.Fatalf("OPERATE inside caller timeout denied: %s", got.Reason)
	}

	clock = newFakeClock()
	gateway = New(Policy{SelectTimeout: time.Second, Clock: clock.Now})
	selectRequest = requestFor(10, 20, selectPayload, 3)
	selectRequest.Timeout = 200 * time.Millisecond
	if got := gateway.Admit(selectRequest); !got.Allowed {
		t.Fatalf("SELECT denied: %s", got.Reason)
	}
	clock.Advance(200 * time.Millisecond)
	expired := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 3), 3))
	if expired.Allowed || expired.Reason != ReasonSelectExpired {
		t.Fatalf("expired OPERATE decision = %v/%s", expired.Allowed, expired.Reason)
	}

	// Once expired, a later request still cannot use the SELECT.
	clock.Advance(time.Second)
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 3), 3)); got.Allowed {
		t.Fatal("expired SELECT was reused")
	}
}

func TestPolicyTimeoutCapsCallerTimeout(t *testing.T) {
	clock := newFakeClock()
	gateway := New(Policy{
		SelectTimeout: 100 * time.Millisecond,
		Clock:         clock.Now,
	})

	selectRequest := requestFor(10, 20, applicationBytes(FCSelect, 4), 4)
	selectRequest.Timeout = time.Second
	if got := gateway.Admit(selectRequest); !got.Allowed {
		t.Fatalf("SELECT denied: %s", got.Reason)
	}

	clock.Advance(100 * time.Millisecond)
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 4), 4)); got.Allowed {
		t.Fatal("caller timeout extended the policy select lifetime")
	}
}

func TestAbsoluteCallerDeadline(t *testing.T) {
	clock := newFakeClock()
	gateway := New(Policy{Clock: clock.Now})

	selectRequest := requestFor(10, 20, applicationBytes(FCSelect, 2), 2)
	selectRequest.Deadline = clock.Now().Add(50 * time.Millisecond)
	if got := gateway.Admit(selectRequest); !got.Allowed {
		t.Fatalf("SELECT denied: %s", got.Reason)
	}

	clock.Advance(50 * time.Millisecond)
	expired := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 2), 2))
	if expired.Allowed || expired.Reason != ReasonSelectExpired {
		t.Fatalf("deadline decision = %v/%s", expired.Allowed, expired.Reason)
	}
}

func TestParseTransportFragmentFromRawFrame(t *testing.T) {
	payload := applicationBytes(FCSelect, 6)
	raw := []byte{transportFIRMask | transportFINMask | 6}
	raw = append(raw, payload...)

	fragment, err := ParseTransportFragment(raw)
	if err != nil {
		t.Fatalf("ParseTransportFragment: %v", err)
	}
	if !fragment.FIR || !fragment.FIN || fragment.Sequence != 6 {
		t.Fatalf("unexpected header: %+v", fragment)
	}
	reassembled, err := ReassembleTransport([]TransportFragment{fragment}, 0)
	if err != nil {
		t.Fatalf("ReassembleTransport: %v", err)
	}
	application, err := ParseApplicationRequest(reassembled)
	if err != nil {
		t.Fatalf("ParseApplicationRequest: %v", err)
	}
	if application.Function != FCSelect || application.Sequence != 6 {
		t.Fatalf("unexpected application header: %+v", application)
	}
}

func TestUnsplitControlOperationStillWorks(t *testing.T) {
	gateway := New(Policy{Clock: newFakeClock().Now})

	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCSelect, 7), 7)); !got.Allowed {
		t.Fatalf("single-frame SELECT denied: %s", got.Reason)
	}
	if got := gateway.Admit(requestFor(10, 20, applicationBytes(FCOperate, 7), 7)); !got.Allowed {
		t.Fatalf("single-frame OPERATE denied: %s", got.Reason)
	}
}
