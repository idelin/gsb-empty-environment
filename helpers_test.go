package sliceauth

import "testing"

var (
	homePLMN  = PLMNID{MCC: "460", MNC: "00"}
	visitA    = PLMNID{MCC: "310", MNC: "260"}
	visitB    = PLMNID{MCC: "234", MNC: "15"}
	sliceEMBB = MustSliceID(1, NoSD)
	sliceURLL = MustSliceID(2, 0x0000AB)
	sliceMIoT = MustSliceID(3, NoSD)
	// sliceURLLConfused has the same SST as the URLLC subscription but a
	// different SD: the canonical SD-confusion probe, which must not match.
	sliceURLLConfused = MustSliceID(2, 0x0000CD)
	sliceGhost        = MustSliceID(99, NoSD)
	// Visited-PLMN mapped S-NSSAIs for the roaming tests.
	mappedEMBB = MustSliceID(11, NoSD)
	mappedURLL = MustSliceID(12, 0x0000AB)
	mappedMIoT = MustSliceID(13, NoSD)
)

func ptr32(v uint32) *uint32 { return &v }

func ptrReason(r RejectReason) *RejectReason { return &r }

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()
	auth, err := New(homePLMN, []SliceSubscription{
		{
			Slice:               sliceEMBB,
			AllowedDNNs:         []string{"internet", "ims"},
			QoSPolicyGeneration: 100,
			Roaming: []RoamingRule{
				{VisitedPLMN: visitA, MappedSlice: mappedEMBB, Allowed: true},
			},
		},
		{
			Slice:               sliceURLL,
			AllowedDNNs:         []string{"urllc"},
			QoSPolicyGeneration: 200,
			Roaming: []RoamingRule{
				{VisitedPLMN: visitA, MappedSlice: mappedURLL, Allowed: true},
			},
		},
		{
			Slice:               sliceMIoT,
			AllowedDNNs:         []string{"iot"},
			QoSPolicyGeneration: 300,
			Roaming: []RoamingRule{
				{VisitedPLMN: visitA, MappedSlice: mappedMIoT, Allowed: false},
			},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return auth
}

func assertAllowedSlices(t *testing.T, grants []GrantedSlice, want []SliceID) {
	t.Helper()
	if len(grants) != len(want) {
		t.Fatalf("allowed slice count = %d, want %d (%+v)", len(grants), len(want), grants)
	}
	got := make(map[SliceID]bool, len(grants))
	for _, grant := range grants {
		got[grant.Slice] = true
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("expected granted slice %s, grants = %+v", id, grants)
		}
	}
}
