package sliceauth

import "testing"

func homeRegistration(t *testing.T, auth *Authorizer, slices []SliceID) Registration {
	t.Helper()
	reg, _, err := auth.AuthorizeRegistration(Registration{
		AMFID:          "amf-1",
		ServingPLMN:    homePLMN,
		RequestedNSSAI: slices,
	})
	if err != nil {
		t.Fatalf("AuthorizeRegistration() error = %v", err)
	}
	return reg
}

func TestAuthorizePDUSession(t *testing.T) {
	auth := newTestAuthorizer(t)
	reg := homeRegistration(t, auth, []SliceID{sliceEMBB, sliceURLL})
	tests := []struct {
		name       string
		session    PDUSession
		wantReason *RejectReason
		wantCause  uint8
	}{
		{
			name: "valid session with matching DNN and QoS generation",
			session: PDUSession{
				ID: 1, ServingSlice: sliceEMBB, SMFID: "smf-1",
				DNN: "internet", QoSPolicyGeneration: 100,
			},
		},
		{
			name: "unsubscribed slice in stale SMF context rejected with 5GSM 68",
			session: PDUSession{
				ID: 2, ServingSlice: sliceGhost, SMFID: "smf-1",
				DNN: "internet", QoSPolicyGeneration: 1,
			},
			wantReason: ptrReason(ReasonSliceNotInAllowedNSSAI),
			wantCause:  Cause5GSMSliceNotInAllowedNSSAI,
		},
		{
			name: "SD-confused slice in session context rejected with 5GSM 68",
			session: PDUSession{
				ID: 3, ServingSlice: sliceURLLConfused, SMFID: "smf-1",
				DNN: "urllc", QoSPolicyGeneration: 200,
			},
			wantReason: ptrReason(ReasonSliceNotInAllowedNSSAI),
			wantCause:  Cause5GSMSliceNotInAllowedNSSAI,
		},
		{
			name: "DNN not subscribed for the slice rejected with 5GSM 70",
			session: PDUSession{
				ID: 4, ServingSlice: sliceURLL, SMFID: "smf-1",
				DNN: "internet", QoSPolicyGeneration: 200,
			},
			wantReason: ptrReason(ReasonDNNNotSubscribed),
			wantCause:  Cause5GSMDNNNotSubscribed,
		},
		{
			name: "stale QoS policy generation rejected with 5GSM 29",
			session: PDUSession{
				ID: 5, ServingSlice: sliceEMBB, SMFID: "smf-old",
				DNN: "ims", QoSPolicyGeneration: 99,
			},
			wantReason: ptrReason(ReasonPolicyGenerationMismatch),
			wantCause:  Cause5GSMUserAuthOrDNNAuthorization,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grant, rejection := auth.AuthorizePDUSession(reg, tt.session)
			if tt.wantReason != nil {
				if rejection == nil {
					t.Fatalf("want rejection %q, got grant %+v", *tt.wantReason, grant)
				}
				if rejection.Reason != *tt.wantReason {
					t.Fatalf("reason = %q, want %q", rejection.Reason, *tt.wantReason)
				}
				if rejection.Cause.Code != tt.wantCause || rejection.Cause.Layer != Layer5GSM {
					t.Fatalf("cause = %s, want 5GSM %d", rejection.Cause, tt.wantCause)
				}
				return
			}
			if rejection != nil {
				t.Fatalf("unexpected rejection: %s", rejection)
			}
			if grant == nil || grant.QoSPolicyGeneration != 100 {
				t.Fatalf("grant must carry subscription QoS generation, got %+v", grant)
			}
		})
	}
}

func TestAMFReselection(t *testing.T) {
	auth := newTestAuthorizer(t)
	reg := homeRegistration(t, auth, []SliceID{sliceEMBB, sliceURLL, sliceMIoT})
	target := AMFCapability{
		AMFID:          "amf-2",
		SupportedNSSAI: []SliceID{sliceEMBB, sliceMIoT, sliceGhost},
	}
	moved, rejected, err := auth.ReselectAMF(reg, target)
	if err != nil {
		t.Fatalf("ReselectAMF() error = %v", err)
	}
	assertAllowedSlices(t, moved.AllowedNSSAI, []SliceID{sliceEMBB, sliceMIoT})
	if moved.AMFID != "amf-2" {
		t.Fatalf("AMF ID = %q, want amf-2", moved.AMFID)
	}
	if len(rejected) != 1 || rejected[0].Slice != sliceURLL ||
		rejected[0].Reason != ReasonSliceUnsupportedByTarget ||
		rejected[0].Cause.Code != Cause5GMMUnsupportedSlice {
		t.Fatalf("URLLC must be dropped with 5GMM 71, got %+v", rejected)
	}
	for _, grant := range moved.AllowedNSSAI {
		if grant.Slice == sliceGhost {
			t.Fatal("target AMF capability must not inject an unsubscribed slice")
		}
	}

	t.Run("handover across AMF requires reselection first", func(t *testing.T) {
		_, err := auth.AuthorizeHandover(reg, HandoverRequest{
			TargetPLMN:            homePLMN,
			TargetAMFID:           "amf-2",
			TargetRANAllowedNSSAI: []SliceID{sliceEMBB},
			Sessions: []PDUSession{{
				ID: 1, ServingSlice: sliceEMBB, DNN: "internet",
				QoSPolicyGeneration: 100,
			}},
		})
		if err == nil {
			t.Fatal("cross-AMF handover without reselection must be a programming error")
		}
	})

	t.Run("reselection only removes, re-registered registration then handover works", func(t *testing.T) {
		decision, err := auth.AuthorizeHandover(moved, HandoverRequest{
			TargetPLMN:            homePLMN,
			TargetAMFID:           "amf-2",
			TargetRANAllowedNSSAI: []SliceID{sliceEMBB, sliceMIoT},
			Sessions: []PDUSession{{
				ID: 1, ServingSlice: sliceEMBB, DNN: "internet",
				QoSPolicyGeneration: 100,
			}},
		})
		if err != nil {
			t.Fatalf("AuthorizeHandover() error = %v", err)
		}
		if len(decision.AllowedSessions) != 1 || len(decision.RejectedSessions) != 0 {
			t.Fatalf("want exactly one admitted session, got %+v", decision)
		}
	})
}
