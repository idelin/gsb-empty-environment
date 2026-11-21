package sliceauth_test

import (
	"fmt"

	"sliceauth"
)

func Example() {
	home, _ := sliceauth.NewPLMNID("460", "00")
	embb, _ := sliceauth.NewSliceID(1, nil)
	urllcSD := uint32(0x0000AB)
	urllc, _ := sliceauth.NewSliceID(2, &urllcSD)

	auth, err := sliceauth.New(home, []sliceauth.SliceSubscription{
		{
			Slice:               embb,
			AllowedDNNs:         []string{"internet"},
			QoSPolicyGeneration: 100,
		},
		{
			Slice:               urllc,
			AllowedDNNs:         []string{"urllc"},
			QoSPolicyGeneration: 200,
		},
	})
	if err != nil {
		panic(err)
	}

	// 1. Registration: the allowed NSSAI is derived, never inherited.
	reg, rejected, err := auth.AuthorizeRegistration(sliceauth.Registration{
		AMFID:          "amf-1",
		ServingPLMN:    home,
		RequestedNSSAI: []sliceauth.SliceID{embb, urllc},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("registered grants:", len(reg.AllowedNSSAI), "rejected:", len(rejected))

	// 2. AMF reselection can only remove slices the target AMF does not support.
	moved, dropped, err := auth.ReselectAMF(reg, sliceauth.AMFCapability{
		AMFID:          "amf-2",
		SupportedNSSAI: []sliceauth.SliceID{embb},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("after reselection:", len(moved.AllowedNSSAI), "dropped:", dropped[0].Cause.Code)

	// 3. Handover: subscription ∩ target RAN NSSAI, per session re-verification.
	decision, err := auth.AuthorizeHandover(moved, sliceauth.HandoverRequest{
		TargetPLMN:            home,
		TargetAMFID:           "amf-2",
		TargetRANAllowedNSSAI: []sliceauth.SliceID{embb},
		Sessions: []sliceauth.PDUSession{{
			ID: 1, ServingSlice: embb, DNN: "internet", QoSPolicyGeneration: 100,
		}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("handover admitted sessions:", len(decision.AllowedSessions),
		"allowed slices:", decision.AllowedSlices()[0].String())

	// Output:
	// registered grants: 2 rejected: 0
	// after reselection: 1 dropped: 71
	// handover admitted sessions: 1 allowed slices: SST=1
}
