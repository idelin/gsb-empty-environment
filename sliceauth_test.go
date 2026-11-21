package sliceauth

import "testing"

func TestSliceIDNormalization(t *testing.T) {
	absent, err := NewSliceID(1, nil)
	if err != nil {
		t.Fatalf("NewSliceID() error = %v", err)
	}
	explicitDefault, err := NewSliceID(1, ptr32(NoSD))
	if err != nil {
		t.Fatalf("NewSliceID() error = %v", err)
	}
	if absent != explicitDefault {
		t.Fatalf("absent SD %s must equal explicit 0xFFFFFF %s", absent, explicitDefault)
	}
	withSD, err := NewSliceID(2, ptr32(0x0000AB))
	if err != nil {
		t.Fatalf("NewSliceID() error = %v", err)
	}
	if withSD == absent {
		t.Fatalf("explicit SD slice %s must not equal no-SD slice %s", withSD, absent)
	}
	if _, err := NewSliceID(0, nil); err == nil {
		t.Fatal("SST 0 must be rejected")
	}
	if _, err := NewSliceID(1, ptr32(NoSD+1)); err == nil {
		t.Fatal("SD above 0xFFFFFF must be rejected")
	}
}

func TestAuthorizeRegistration(t *testing.T) {
	auth := newTestAuthorizer(t)
	tests := []struct {
		name         string
		plmn         PLMNID
		requested    []SliceID
		wantAllowed  []SliceID
		wantReject   *RejectReason
		wantCause    uint8
		wantError    bool
		prepopulated bool
	}{
		{
			name:        "legitimate subscribed slices at home",
			plmn:        homePLMN,
			requested:   []SliceID{sliceEMBB, sliceURLL},
			wantAllowed: []SliceID{sliceEMBB, sliceURLL},
		},
		{
			name:       "unsubscribed slice rejected with 5GMM 71",
			plmn:       homePLMN,
			requested:  []SliceID{sliceGhost},
			wantReject: ptrReason(ReasonSliceNotSubscribed),
			wantCause:  Cause5GMMSliceNotSubscribed,
		},
		{
			name:       "SD confusion same SST rejected as SD mismatch",
			plmn:       homePLMN,
			requested:  []SliceID{sliceURLLConfused},
			wantReject: ptrReason(ReasonSDMismatch),
			wantCause:  Cause5GMMSliceNotSubscribed,
		},
		{
			name:        "roaming mapped slices allowed at visited PLMN",
			plmn:        visitA,
			requested:   []SliceID{mappedEMBB, mappedURLL},
			wantAllowed: []SliceID{mappedEMBB, mappedURLL},
		},
		{
			name:       "home-only slice used while roaming barred with 5GMM 13",
			plmn:       visitA,
			requested:  []SliceID{sliceMIoT},
			wantReject: ptrReason(ReasonRoamingRestricted),
			wantCause:  Cause5GMMRoamingNotAllowed,
		},
		{
			name:       "explicitly barred mapped MIoT rejected with 5GMM 13",
			plmn:       visitA,
			requested:  []SliceID{mappedMIoT},
			wantReject: ptrReason(ReasonRoamingRestricted),
			wantCause:  Cause5GMMRoamingNotAllowed,
		},
		{
			name:       "visited PLMN with no mapping rejects roaming",
			plmn:       visitB,
			requested:  []SliceID{sliceEMBB},
			wantReject: ptrReason(ReasonRoamingRestricted),
			wantCause:  Cause5GMMRoamingNotAllowed,
		},
		{
			name:       "SD mismatch on mapped URLLC SST while roaming",
			plmn:       visitA,
			requested:  []SliceID{MustSliceID(12, 0x0000CD)},
			wantReject: ptrReason(ReasonSDMismatch),
			wantCause:  Cause5GMMSliceNotSubscribed,
		},
		{
			name:       "SD confusion on a mapped SST rejected as SD mismatch",
			plmn:       visitA,
			requested:  []SliceID{MustSliceID(11, 0x000001)},
			wantReject: ptrReason(ReasonSDMismatch),
			wantCause:  Cause5GMMSliceNotSubscribed,
		},
		{
			name:         "pre-populated allowed NSSAI inherited from old AMF refused",
			plmn:         homePLMN,
			requested:    []SliceID{sliceEMBB},
			prepopulated: true,
			wantError:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := Registration{
				AMFID:          "amf-1",
				ServingPLMN:    tt.plmn,
				RequestedNSSAI: tt.requested,
			}
			if tt.prepopulated {
				reg.AllowedNSSAI = []GrantedSlice{{Slice: sliceGhost}}
			}
			out, rejected, err := auth.AuthorizeRegistration(reg)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got decision %+v", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthorizeRegistration() error = %v", err)
			}
			assertAllowedSlices(t, out.AllowedNSSAI, tt.wantAllowed)
			if tt.wantReject != nil {
				if len(rejected) != 1 {
					t.Fatalf("want 1 rejection, got %d", len(rejected))
				}
				if rejected[0].Reason != *tt.wantReject {
					t.Fatalf("reason = %q, want %q", rejected[0].Reason, *tt.wantReject)
				}
				if rejected[0].Cause.Code != tt.wantCause || rejected[0].Cause.Layer != Layer5GMM {
					t.Fatalf("cause = %s, want 5GMM %d", rejected[0].Cause, tt.wantCause)
				}
				if len(out.AllowedNSSAI) != 0 {
					t.Fatalf("rejected slice must not be granted, got %+v", out.AllowedNSSAI)
				}
			} else if len(rejected) != 0 {
				t.Fatalf("unexpected rejections: %+v", rejected)
			}
		})
	}
}
