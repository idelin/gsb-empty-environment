package netsec_test

import (
	"testing"

	netsec "example.org/netsec/q0237"
)

func TestComponentIsImportable(t *testing.T) {
	slice := netsec.SNSSAI{SST: 1, SD: "000001"}
	outsider := netsec.SNSSAI{SST: 9, SD: "000009"}

	authorizer := netsec.NewAuthorizer(netsec.Subscription{Slices: []netsec.SlicePolicy{{
		SNSSAI: slice,
		DNN:    "internet",
	}}})

	decision := authorizer.AuthorizeHandover(netsec.HandoverRequest{
		Registration: netsec.Registration{AllowedSlices: []netsec.SNSSAI{slice, outsider}},
		PDUSession: netsec.PDUSession{
			Slices: []netsec.SNSSAI{slice, outsider},
			DNN:    "internet",
		},
		TargetAllowedSlices: []netsec.SNSSAI{slice, outsider},
	})

	if len(decision.Allowed) != 1 || !decision.Allowed[0].Equal(slice) {
		t.Fatalf("unexpected allowed set: %v", decision.Allowed)
	}
	if len(decision.Rejected) != 1 {
		t.Fatalf("unexpected rejection set: %+v", decision.Rejected)
	}
	if decision.Rejected[0].Cause != netsec.CauseRequestedServiceOptionNotSubscribed {
		t.Fatalf("unexpected cause: %d", decision.Rejected[0].Cause)
	}
}
