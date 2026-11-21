package sliceauth

import "testing"

func validSession(id uint32, slice SliceID, dnn string, gen uint64) PDUSession {
	return PDUSession{ID: id, ServingSlice: slice, SMFID: "smf-1", DNN: dnn, QoSPolicyGeneration: gen}
}

func TestAuthorizeHandover(t *testing.T) {
	auth := newTestAuthorizer(t)
	reg := homeRegistration(t, auth, []SliceID{sliceEMBB, sliceURLL, sliceMIoT})

	tests := []struct {
		name             string
		req              HandoverRequest
		wantAllowed      []SliceID
		wantRejectedIDs  []uint32
		wantReason       RejectReason
		wantCause        uint8
		wantNewReject    *RejectReason
		wantOverallCause bool
	}{
		{
			name: "legitimate same-subscription sessions admitted",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceEMBB, sliceURLL, sliceMIoT},
				Sessions: []PDUSession{
					validSession(1, sliceEMBB, "internet", 100),
					validSession(2, sliceURLL, "urllc", 200),
				},
			},
			wantAllowed: []SliceID{sliceEMBB, sliceURLL},
		},
		{
			name: "partial allow: only the target RAN intersection survives",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceEMBB},
				Sessions: []PDUSession{
					validSession(1, sliceEMBB, "internet", 100),
					validSession(2, sliceURLL, "urllc", 200),
				},
			},
			wantAllowed:     []SliceID{sliceEMBB},
			wantRejectedIDs: []uint32{2},
			wantReason:      ReasonSliceUnsupportedByTarget,
			wantCause:       Cause5GSMUnsupportedSlice,
		},
		{
			name: "unsubscribed slice in old SMF context cannot be handed over",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceEMBB, sliceGhost},
				Sessions: []PDUSession{
					validSession(7, sliceGhost, "internet", 1),
				},
			},
			wantRejectedIDs:  []uint32{7},
			wantReason:       ReasonSliceNotInAllowedNSSAI,
			wantCause:        Cause5GSMSliceNotInAllowedNSSAI,
			wantOverallCause: true,
		},
		{
			name: "SD confusion in session context is denied even if target RAN offers it",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceURLLConfused},
				Sessions: []PDUSession{
					validSession(8, sliceURLLConfused, "urllc", 200),
				},
			},
			wantRejectedIDs:  []uint32{8},
			wantReason:       ReasonSliceNotInAllowedNSSAI,
			wantCause:        Cause5GSMSliceNotInAllowedNSSAI,
			wantOverallCause: true,
		},
		{
			name: "stale QoS generation from old SMF forces re-subscription check",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceMIoT},
				Sessions: []PDUSession{
					validSession(9, sliceMIoT, "iot", 299),
				},
			},
			wantRejectedIDs:  []uint32{9},
			wantReason:       ReasonPolicyGenerationMismatch,
			wantCause:        Cause5GSMUserAuthOrDNNAuthorization,
			wantOverallCause: true,
		},
		{
			name: "new slice subscribed but absent at target RAN is denied with 5GMM 71",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceEMBB},
				Sessions: []PDUSession{
					validSession(1, sliceEMBB, "internet", 100),
				},
				NewSlices: []SliceID{sliceURLL},
			},
			wantAllowed:   []SliceID{sliceEMBB},
			wantNewReject: ptrReason(ReasonSliceUnsupportedByTarget),
		},
		{
			name: "new unsubscribed slice is re-checked against subscription and denied",
			req: HandoverRequest{
				TargetPLMN:            homePLMN,
				TargetAMFID:           "amf-1",
				TargetRANAllowedNSSAI: []SliceID{sliceEMBB, sliceGhost},
				Sessions: []PDUSession{
					validSession(1, sliceEMBB, "internet", 100),
				},
				NewSlices: []SliceID{sliceGhost},
			},
			wantAllowed:   []SliceID{sliceEMBB},
			wantNewReject: ptrReason(ReasonSliceNotSubscribed),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := auth.AuthorizeHandover(reg, tt.req)
			if err != nil {
				t.Fatalf("AuthorizeHandover() error = %v", err)
			}
			assertSlices(t, decision.AllowedSlices(), tt.wantAllowed)
			if len(decision.RejectedSessions) != len(tt.wantRejectedIDs) {
				t.Fatalf("rejected session count = %d, want %d (%+v)",
					len(decision.RejectedSessions), len(tt.wantRejectedIDs), decision.RejectedSessions)
			}
			for i, wantID := range tt.wantRejectedIDs {
				rejected := decision.RejectedSessions[i]
				if rejected.PDUSessionID != wantID {
					t.Fatalf("rejected session ID = %d, want %d", rejected.PDUSessionID, wantID)
				}
				if rejected.Reason != tt.wantReason {
					t.Fatalf("reason = %q, want %q", rejected.Reason, tt.wantReason)
				}
				if rejected.Cause.Code != tt.wantCause || rejected.Cause.Layer != Layer5GSM {
					t.Fatalf("cause = %s, want 5GSM %d", rejected.Cause, tt.wantCause)
				}
			}
			if tt.wantNewReject != nil {
				if len(decision.RejectedNewSlices) != 1 {
					t.Fatalf("want 1 rejected new slice, got %+v", decision.RejectedNewSlices)
				}
				rejected := decision.RejectedNewSlices[0]
				if rejected.Reason != *tt.wantNewReject {
					t.Fatalf("new slice reason = %q, want %q", rejected.Reason, *tt.wantNewReject)
				}
				if rejected.Cause.Layer != Layer5GMM {
					t.Fatalf("new slice cause layer = %s, want 5GMM", rejected.Cause.Layer)
				}
			}
			if tt.wantOverallCause {
				if decision.OverallRejection == nil ||
					decision.OverallRejection.Cause.Code != Cause5GMMNoNetworkSlices {
					t.Fatalf("overall rejection must map to 5GMM 62, got %+v", decision.OverallRejection)
				}
			} else if decision.OverallRejection != nil {
				t.Fatalf("unexpected overall rejection: %s", decision.OverallRejection)
			}
		})
	}
}

func assertSlices(t *testing.T, got, want []SliceID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice count = %d, want %d (%v)", len(got), len(want), got)
	}
	seen := make(map[SliceID]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("missing allowed slice %s, got %v", id, got)
		}
	}
}

func TestRoamingHandoverEnforcesRoamingRestriction(t *testing.T) {
	auth := newTestAuthorizer(t)
	reg, rejected, err := auth.AuthorizeRegistration(Registration{
		AMFID:          "amf-visit",
		ServingPLMN:    visitA,
		RequestedNSSAI: []SliceID{mappedEMBB, mappedURLL},
	})
	if err != nil {
		t.Fatalf("AuthorizeRegistration() error = %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("roaming registration rejected unexpectedly: %+v", rejected)
	}
	decision, err := auth.AuthorizeHandover(reg, HandoverRequest{
		TargetPLMN:            visitA,
		TargetAMFID:           "amf-visit",
		TargetRANAllowedNSSAI: []SliceID{mappedEMBB, mappedURLL, sliceMIoT},
		Sessions: []PDUSession{
			validSession(1, mappedEMBB, "internet", 100),
			// Session continuity attempt with a home-only slice while roaming.
			validSession(2, sliceMIoT, "iot", 300),
		},
	})
	if err != nil {
		t.Fatalf("AuthorizeHandover() error = %v", err)
	}
	assertSlices(t, decision.AllowedSlices(), []SliceID{mappedEMBB})
	if len(decision.RejectedSessions) != 1 {
		t.Fatalf("want 1 rejected session, got %+v", decision.RejectedSessions)
	}
	rejection := decision.RejectedSessions[0]
	if rejection.Reason != ReasonSliceNotInAllowedNSSAI ||
		rejection.Cause.Code != Cause5GSMSliceNotInAllowedNSSAI {
		t.Fatalf("roaming session-continuity bypass must map to 5GSM 68, got %s", rejection)
	}
}

func TestNoWholesaleCopyToTarget(t *testing.T) {
	auth := newTestAuthorizer(t)
	reg := homeRegistration(t, auth, []SliceID{sliceEMBB, sliceURLL, sliceMIoT})

	// The target cell (potentially attacker-controlled) only advertises eMBB.
	intersection, err := AllowedTargetSlices(reg, []SliceID{sliceEMBB, sliceGhost})
	if err != nil {
		t.Fatalf("AllowedTargetSlices() error = %v", err)
	}
	assertSlices(t, intersection, []SliceID{sliceEMBB})

	decision, err := auth.AuthorizeHandover(reg, HandoverRequest{
		TargetPLMN:            homePLMN,
		TargetAMFID:           "amf-1",
		TargetRANAllowedNSSAI: []SliceID{sliceEMBB},
		Sessions: []PDUSession{
			validSession(1, sliceEMBB, "internet", 100),
			validSession(2, sliceURLL, "urllc", 200),
			validSession(3, sliceMIoT, "iot", 300),
		},
	})
	if err != nil {
		t.Fatalf("AuthorizeHandover() error = %v", err)
	}
	assertSlices(t, decision.AllowedSlices(), []SliceID{sliceEMBB})
	if len(decision.RejectedSessions) != 2 {
		t.Fatalf("the two non-intersecting sessions must be dropped, got %+v", decision.RejectedSessions)
	}
	for _, id := range decision.AllowedSlices() {
		if id != sliceEMBB {
			t.Fatalf("only the target RAN intersection may be advertised, found %s", id)
		}
	}
}
