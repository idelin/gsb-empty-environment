package netsec

import (
	"slices"
	"testing"
)

func testSlice(sst uint8, sd string) SNSSAI {
	return SNSSAI{SST: sst, SD: sd}
}

func requireAllowed(t *testing.T, decision Decision, want ...SNSSAI) {
	t.Helper()
	if !slices.Equal(decision.Allowed, want) {
		t.Fatalf("allowed = %v, want %v", decision.Allowed, want)
	}
}

func requireRejection(t *testing.T, decision Decision, slice SNSSAI, cause CauseCode, reason RejectionReason) {
	t.Helper()
	for _, rejection := range decision.Rejected {
		if rejection.SNSSAI == slice || rejection.SNSSAI.Equal(slice) {
			if rejection.Cause != cause || rejection.Reason != reason {
				t.Fatalf("rejection for %s = cause %d reason %q, want cause %d reason %q",
					slice, rejection.Cause, rejection.Reason, cause, reason)
			}
			return
		}
	}
	t.Fatalf("missing rejection for %s; got %+v", slice, decision.Rejected)
}

func TestHandoverDropsSliceOutsideSubscription(t *testing.T) {
	allowed := testSlice(1, "000001")
	unsubscribed := testSlice(2, "000002")

	subscription := Subscription{Slices: []SlicePolicy{
		{SNSSAI: allowed},
	}}
	decision := AuthorizeHandover(HandoverRequest{
		Subscription: subscription,
		PDUSession: PDUSession{
			Slices: []SNSSAI{allowed, unsubscribed},
		},
		TargetAllowedSlices: []SNSSAI{allowed, unsubscribed},
	})

	requireAllowed(t, decision, allowed)
	requireRejection(t, decision, unsubscribed, CauseRequestedServiceOptionNotSubscribed, ReasonSliceNotSubscribed)
	if len(decision.Rejected) != 1 || decision.Complete() {
		t.Fatalf("expected partial decision with one denial, got %+v", decision)
	}
}

func TestHandoverRejectsSDConfusion(t *testing.T) {
	subscribed := testSlice(1, "000001")
	confused := testSlice(1, "000002")
	shortSD := testSlice(1, "1")
	absentSD := testSlice(1, "")

	subscription := Subscription{Slices: []SlicePolicy{{SNSSAI: subscribed}}}
	target := []SNSSAI{confused, shortSD, absentSD}

	decision := AuthorizeHandover(HandoverRequest{
		Subscription:        subscription,
		RequestedSlices:     target,
		TargetAllowedSlices: target,
	})

	if len(decision.Allowed) != 0 {
		t.Fatalf("SD variants must not inherit the subscribed slice: %v", decision.Allowed)
	}
	requireRejection(t, decision, confused, CauseRequestedServiceOptionNotSubscribed, ReasonSDMismatch)
	requireRejection(t, decision, shortSD, CauseRequestRejectedUnspecified, ReasonInvalidSNSSAI)
	requireRejection(t, decision, absentSD, CauseRequestedServiceOptionNotSubscribed, ReasonSDMismatch)
}

func TestSDHexCaseIsCanonicalizedButBytesRemainExact(t *testing.T) {
	upper := testSlice(1, "ABCDEF")
	lower := testSlice(1, "abcdef")

	subscription := Subscription{Slices: []SlicePolicy{{SNSSAI: upper}}}
	decision := AuthorizeSlices(SliceRequest{
		Subscription: subscription,
		Slices:       []SNSSAI{lower},
	})

	requireAllowed(t, decision, upper)
	if !decision.OK() {
		t.Fatalf("same SD bytes should match: %+v", decision)
	}
}

func TestHandoverEnforcesRoamingRestriction(t *testing.T) {
	slice := testSlice(1, "000001")
	network := ServingNetwork{HomePLMN: "310-410", ServingPLMN: "262-01"}

	t.Run("roaming denied", func(t *testing.T) {
		subscription := Subscription{Slices: []SlicePolicy{{
			SNSSAI:         slice,
			RoamingAllowed: false,
		}}}
		decision := AuthorizeHandover(HandoverRequest{
			Subscription:        subscription,
			RequestedSlices:     []SNSSAI{slice},
			TargetAllowedSlices: []SNSSAI{slice},
			ServingNetwork:      network,
		})

		if len(decision.Allowed) != 0 {
			t.Fatalf("roaming-restricted slice was allowed: %v", decision.Allowed)
		}
		requireRejection(t, decision, slice, CauseRoamingNotAllowedInTrackingArea, ReasonRoamingRestricted)
	})

	t.Run("roaming allowed", func(t *testing.T) {
		subscription := Subscription{Slices: []SlicePolicy{{
			SNSSAI:         slice,
			RoamingAllowed: true,
		}}}
		decision := AuthorizeHandover(HandoverRequest{
			Subscription:        subscription,
			RequestedSlices:     []SNSSAI{slice},
			TargetAllowedSlices: []SNSSAI{slice},
			ServingNetwork:      network,
		})
		requireAllowed(t, decision, slice)
	})

	t.Run("PLMN allow-list overrides and restricts", func(t *testing.T) {
		allowedList := Subscription{Slices: []SlicePolicy{{
			SNSSAI:         slice,
			RoamingAllowed: false,
			AllowedPLMNs:   []string{"26201"},
		}}}
		decision := AuthorizePDUSession(PDUSessionRequest{
			Subscription:   allowedList,
			PDUSession:     PDUSession{Slices: []SNSSAI{slice}},
			ServingNetwork: network,
		})
		requireAllowed(t, decision, slice)

		restricted := Subscription{Slices: []SlicePolicy{{
			SNSSAI:         slice,
			RoamingAllowed: true,
			AllowedPLMNs:   []string{"20801"},
		}}}
		decision = AuthorizePDUSession(PDUSessionRequest{
			Subscription:   restricted,
			PDUSession:     PDUSession{Slices: []SNSSAI{slice}},
			ServingNetwork: network,
		})
		if len(decision.Allowed) != 0 {
			t.Fatalf("explicit PLMN allow-list should restrict roaming: %v", decision.Allowed)
		}
		requireRejection(t, decision, slice, CauseRoamingNotAllowedInTrackingArea, ReasonRoamingRestricted)
	})
}

func TestCrossAMFReselectionDoesNotInheritOldSlices(t *testing.T) {
	current := testSlice(1, "000001")
	stale := testSlice(2, "000002")

	subscription := Subscription{Slices: []SlicePolicy{{SNSSAI: current}}}
	decision := AuthorizeHandover(HandoverRequest{
		Subscription:        subscription,
		PDUSession:          PDUSession{Slices: []SNSSAI{current}},
		OldAllowedSlices:    []SNSSAI{stale},
		TargetAllowedSlices: []SNSSAI{current, stale},
		AMFReselected:       true,
		SessionContinuity:   true,
		OldSMFContext: &SMFContext{
			AllowedSlices: []SNSSAI{stale},
		},
	})

	requireAllowed(t, decision, current)
	if !decision.Reauthorized {
		t.Fatal("decision should record that inherited context was re-authorized")
	}
	for _, allowed := range decision.Allowed {
		if allowed.Equal(stale) {
			t.Fatalf("old slice was inherited without subscription authority: %v", decision.Allowed)
		}
	}
}

func TestOldSMFContextOnlyIsRejectedWhenOutsideSubscription(t *testing.T) {
	stale := testSlice(2, "000002")
	decision := AuthorizeHandover(HandoverRequest{
		Subscription: Subscription{Slices: []SlicePolicy{
			{SNSSAI: testSlice(1, "000001")},
		}},
		TargetAllowedSlices: []SNSSAI{stale},
		OldSMFContext: &SMFContext{
			AllowedSlices: []SNSSAI{stale},
		},
		SessionContinuity: true,
	})

	if len(decision.Allowed) != 0 {
		t.Fatalf("old SMF context slice leaked into target network: %v", decision.Allowed)
	}
	requireRejection(t, decision, stale, CauseRequestedServiceOptionNotSubscribed, ReasonSliceNotSubscribed)
	if !decision.Reauthorized {
		t.Fatal("old SMF context should force reauthorization")
	}
}

func TestLegalSameSubscriptionHandover(t *testing.T) {
	slice := testSlice(1, "abcdef")
	subscription := Subscription{Slices: []SlicePolicy{{
		SNSSAI:         slice,
		RoamingAllowed: true,
		DNN:            "internet",
		QoSGeneration:  7,
	}}}

	decision := AuthorizeHandover(HandoverRequest{
		Subscription:        subscription,
		Registration:        Registration{AllowedSlices: []SNSSAI{testSlice(1, "ABCDEF")}},
		PDUSession:          PDUSession{Slices: []SNSSAI{testSlice(1, "ABCDEF")}, DNN: "internet", QoSGeneration: 7},
		ServingNetwork:      ServingNetwork{HomePLMN: "310410", ServingPLMN: "310410"},
		TargetAllowedSlices: []SNSSAI{testSlice(1, "ABCDEF")},
	})

	requireAllowed(t, decision, testSlice(1, "ABCDEF"))
	if !decision.Complete() || !decision.OK() || !decision.AnyAllowed() {
		t.Fatalf("legal handover should be fully authorized: %+v", decision)
	}
}

func TestPartialAllowIntersectsSubscriptionAndTarget(t *testing.T) {
	allowed := testSlice(1, "000001")
	notSubscribed := testSlice(2, "000002")
	notAllowedByTarget := testSlice(3, "000003")

	subscription := Subscription{Slices: []SlicePolicy{
		{SNSSAI: allowed},
		{SNSSAI: notAllowedByTarget},
	}}
	decision := AuthorizeHandover(HandoverRequest{
		Subscription:        subscription,
		RequestedSlices:     []SNSSAI{allowed, notSubscribed, notAllowedByTarget},
		TargetAllowedSlices: []SNSSAI{allowed, notSubscribed},
	})

	requireAllowed(t, decision, allowed)
	requireRejection(t, decision, notSubscribed, CauseRequestedServiceOptionNotSubscribed, ReasonSliceNotSubscribed)
	requireRejection(t, decision, notAllowedByTarget, CauseServiceOptionNotSupported, ReasonTargetAccessNetworkNotAllowed)
	if decision.Complete() || decision.OK() {
		t.Fatalf("partial allow must not be reported as complete: %+v", decision)
	}
	if decision.Err() == nil {
		t.Fatal("partial allow should expose rejections as an error")
	}
}

func TestNewSlicesAreRecheckedAgainstSubscription(t *testing.T) {
	existing := testSlice(1, "000001")
	newButNotSubscribed := testSlice(2, "000002")
	subscription := Subscription{Slices: []SlicePolicy{{SNSSAI: existing}}}

	decision := AuthorizeHandover(HandoverRequest{
		Subscription:        subscription,
		PDUSession:          PDUSession{Slices: []SNSSAI{existing}},
		RequestedSlices:     []SNSSAI{newButNotSubscribed},
		TargetAllowedSlices: []SNSSAI{existing, newButNotSubscribed},
	})

	requireAllowed(t, decision, existing)
	requireRejection(t, decision, newButNotSubscribed, CauseRequestedServiceOptionNotSubscribed, ReasonSliceNotSubscribed)
}

func TestRegistrationAndPDUSessionBindings(t *testing.T) {
	slice := testSlice(1, "000001")
	other := testSlice(2, "000002")
	subscription := Subscription{Slices: []SlicePolicy{{
		SNSSAI:         slice,
		DNN:            "internet",
		QoSGeneration:  4,
		RoamingAllowed: true,
	}}}

	t.Run("registration does not require DNN binding", func(t *testing.T) {
		decision := AuthorizeRegistration(RegistrationRequest{
			Subscription: subscription,
			Registration: Registration{AllowedSlices: []SNSSAI{slice, other}},
		})
		requireAllowed(t, decision, slice)
		requireRejection(t, decision, other, CauseRequestedServiceOptionNotSubscribed, ReasonSliceNotSubscribed)
	})

	t.Run("DNN mismatch", func(t *testing.T) {
		decision := AuthorizePDUSession(PDUSessionRequest{
			Subscription: subscription,
			PDUSession: PDUSession{
				Slices:        []SNSSAI{slice},
				DNN:           "ims",
				QoSGeneration: 4,
			},
		})
		if len(decision.Allowed) != 0 {
			t.Fatalf("DNN confusion was allowed: %v", decision.Allowed)
		}
		requireRejection(t, decision, slice, CauseMissingOrUnknownDNN, ReasonDNNMismatch)
	})

	t.Run("QoS policy generation mismatch", func(t *testing.T) {
		decision := AuthorizePDUSession(PDUSessionRequest{
			Subscription: subscription,
			PDUSession: PDUSession{
				Slices:        []SNSSAI{slice},
				DNN:           "internet",
				QoSGeneration: 3,
			},
		})
		if len(decision.Allowed) != 0 {
			t.Fatalf("stale QoS generation was allowed: %v", decision.Allowed)
		}
		requireRejection(t, decision, slice, CauseRequestRejectedUnspecified, ReasonQoSGenerationMismatch)
	})

	t.Run("matching binding", func(t *testing.T) {
		decision := AuthorizePDUSession(PDUSessionRequest{
			Subscription: subscription,
			PDUSession: PDUSession{
				Slices:        []SNSSAI{slice},
				DNN:           "internet",
				QoSGeneration: 4,
			},
		})
		requireAllowed(t, decision, slice)
	})
}

func TestRegistrationWithoutAllowedSlicesFailsClosed(t *testing.T) {
	decision := AuthorizeRegistration(RegistrationRequest{
		Subscription: Subscription{Slices: []SlicePolicy{{SNSSAI: testSlice(1, "000001")}}},
	})
	if decision.AnyAllowed() || decision.Complete() {
		t.Fatalf("empty registration must not be treated as complete: %+v", decision)
	}
	if decision.Cause != CauseNoNetworkSlicesAvailable {
		t.Fatalf("empty registration cause = %d, want %d", decision.Cause, CauseNoNetworkSlicesAvailable)
	}
	if decision.Err() == nil {
		t.Fatal("empty registration must fail closed")
	}
}

func TestHandoverWithoutTargetAllowedSetFailsClosed(t *testing.T) {
	slice := testSlice(1, "000001")
	decision := AuthorizeHandover(HandoverRequest{
		Subscription:    Subscription{Slices: []SlicePolicy{{SNSSAI: slice}}},
		RequestedSlices: []SNSSAI{slice},
	})
	if len(decision.Allowed) != 0 {
		t.Fatalf("handover with no target allow-list was permitted: %v", decision.Allowed)
	}
	requireRejection(t, decision, slice, CauseServiceOptionNotSupported, ReasonTargetAccessNetworkNotAllowed)
}

func TestAuthorizerDefaultSubscriptionAndPolicyAliases(t *testing.T) {
	slice := testSlice(1, "000001")
	authorizer := NewSliceAuthorizer(Subscription{AllowedSlices: []SlicePolicy{{
		Slice:          slice,
		QoSVersion:     9,
		RoamingAllowed: true,
	}}})
	decision := authorizer.AuthorizeHandover(HandoverRequest{
		RequestedSlices:     []SNSSAI{slice},
		TargetAllowedSlices: []SNSSAI{slice},
		PDUSession:          PDUSession{QoSGeneration: 9},
	})
	requireAllowed(t, decision, slice)
}

func TestMalformedRegistrationFailsClosed(t *testing.T) {
	slice := testSlice(1, "000001")
	decision := AuthorizePDUSession(PDUSessionRequest{
		Subscription: Subscription{Slices: []SlicePolicy{{SNSSAI: slice}}},
		Registration: Registration{AllowedSlices: []SNSSAI{
			testSlice(1, "not-hex"),
		}},
		PDUSession: PDUSession{Slices: []SNSSAI{slice}},
	})

	if len(decision.Allowed) != 0 {
		t.Fatalf("malformed registration allow-set was ignored: %v", decision.Allowed)
	}
	requireRejection(t, decision, slice, CauseServiceOptionNotSupported, ReasonRegistrationNotAllowed)
}

func TestConflictingSubscriptionPoliciesFailClosed(t *testing.T) {
	slice := testSlice(1, "000001")
	decision := AuthorizeSlices(SliceRequest{
		Subscription: Subscription{Slices: []SlicePolicy{
			{SNSSAI: slice, DNN: "internet"},
			{SNSSAI: slice, DNN: "ims"},
		}},
		Slices: []SNSSAI{slice},
	})

	if len(decision.Allowed) != 0 {
		t.Fatalf("conflicting subscription policies were authorized: %v", decision.Allowed)
	}
	requireRejection(t, decision, slice, CauseRequestRejectedUnspecified, ReasonSubscriptionPolicyConflict)
}
