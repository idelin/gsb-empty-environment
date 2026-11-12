package netsec

import (
	"net"
	"sync"
	"testing"
)

var (
	openBSSID  = testMAC("02:00:00:00:00:01")
	oweBSSID   = testMAC("02:00:00:00:00:02")
	otherBSSID = testMAC("02:00:00:00:00:03")
)

func testMAC(value string) net.HardwareAddr {
	bssid, err := net.ParseMAC(value)
	if err != nil {
		panic(err)
	}
	return bssid
}

func transitionTo(bssid net.HardwareAddr, ssid string) *TransitionMode {
	return &TransitionMode{BSSID: bssid, SSID: ssid}
}

func verifiedPair(openSSID, oweSSID string) []BSS {
	return []BSS{
		{
			BSSID:      openBSSID,
			SSID:       openSSID,
			Security:   SecurityOpen,
			RSSI:       -40,
			Transition: transitionTo(oweBSSID, oweSSID),
		},
		{
			BSSID:      oweBSSID,
			SSID:       oweSSID,
			Security:   SecurityOWE,
			RSSI:       -50,
			Transition: transitionTo(openBSSID, openSSID),
		},
	}
}

func TestSelectVerifiedPair(t *testing.T) {
	decision := Select(verifiedPair("open-net", "owe-net"), Policy{})
	if decision.Action != ActionConnectOWE {
		t.Fatalf("action = %s, want %s (%s)", decision.Action, ActionConnectOWE, decision.Detail)
	}
	if decision.BSS == nil || !sameBSSID(decision.BSS.BSSID, oweBSSID) {
		t.Fatalf("selected BSS = %v, want OWE BSSID %s", decision.BSS, oweBSSID)
	}
	if decision.Pair == nil || !sameBSSID(decision.Pair.Open.BSSID, openBSSID) ||
		!sameBSSID(decision.Pair.OWE.BSSID, oweBSSID) {
		t.Fatalf("pair = %#v, want open/OWE pair", decision.Pair)
	}
	if decision.Reason != ReasonVerifiedOWEPair {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonVerifiedOWEPair)
	}
	if decision.Fallback {
		t.Fatal("verified OWE pair must not be marked as a fallback")
	}
}

func TestOneWayOpenClaimBlocksByDefault(t *testing.T) {
	scan := []BSS{
		{
			BSSID:      openBSSID,
			SSID:       "open-net",
			Security:   SecurityOpen,
			Transition: transitionTo(oweBSSID, "owe-net"),
		},
		{
			BSSID:    oweBSSID,
			SSID:     "owe-net",
			Security: SecurityOWE,
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonOneWayTransition {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonOneWayTransition)
	}
}

func TestOneWayOWEClaimBlocksByDefault(t *testing.T) {
	scan := []BSS{
		{
			BSSID:    openBSSID,
			SSID:     "open-net",
			Security: SecurityOpen,
		},
		{
			BSSID:      oweBSSID,
			SSID:       "owe-net",
			Security:   SecurityOWE,
			Transition: transitionTo(openBSSID, "open-net"),
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonOneWayTransition {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonOneWayTransition)
	}
}

func TestOneWayClaimAllowsExplicitFallback(t *testing.T) {
	scan := []BSS{
		{
			BSSID:      openBSSID,
			SSID:       "open-net",
			Security:   SecurityOpen,
			Transition: transitionTo(oweBSSID, "owe-net"),
		},
		{
			BSSID:    oweBSSID,
			SSID:     "owe-net",
			Security: SecurityOWE,
		},
	}
	decision := Select(scan, Policy{AllowOpenFallback: true})
	if decision.Action != ActionConnectOpen {
		t.Fatalf("action = %s, want explicit open fallback: %+v", decision.Action, decision)
	}
	if !decision.Fallback || decision.Reason != ReasonOpenFallback {
		t.Fatalf("fallback=%t reason=%s, want explicit fallback", decision.Fallback, decision.Reason)
	}
	if decision.BSS == nil || !sameBSSID(decision.BSS.BSSID, openBSSID) {
		t.Fatalf("selected BSS = %v, want open BSSID", decision.BSS)
	}
}

func TestTransitionBSSIDMismatchBlocks(t *testing.T) {
	scan := []BSS{
		{
			BSSID:      openBSSID,
			SSID:       "open-net",
			Security:   SecurityOpen,
			Transition: transitionTo(otherBSSID, "owe-net"),
		},
		{
			BSSID:      oweBSSID,
			SSID:       "owe-net",
			Security:   SecurityOWE,
			Transition: transitionTo(openBSSID, "open-net"),
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.BSS != nil {
		t.Fatalf("blocked decision selected %v", decision.BSS)
	}
}

func TestReverseTransitionBSSIDMismatchBlocks(t *testing.T) {
	scan := verifiedPair("open-net", "owe-net")
	scan[1].Transition.BSSID = otherBSSID

	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonBSSIDMismatch && decision.Reason != ReasonTransitionTargetAbsent {
		t.Fatalf("reason = %s, want a BSSID mismatch reason", decision.Reason)
	}
}

func TestTransitionSSIDMismatchBlocks(t *testing.T) {
	scan := verifiedPair("open-net", "owe-net")
	scan[1].SSID = "evil-owe-net"

	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonSSIDMismatch {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonSSIDMismatch)
	}
}

func TestReverseTransitionSSIDMismatchBlocks(t *testing.T) {
	scan := verifiedPair("open-net", "owe-net")
	scan[1].Transition.SSID = "evil-open-net"

	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonSSIDMismatch {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonSSIDMismatch)
	}
}

func TestForgedTransitionTargetBlocks(t *testing.T) {
	scan := verifiedPair("open-net", "owe-net")
	scan[0].Transition.BSSID = testMAC("02:00:00:00:00:aa")

	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonTransitionTargetAbsent {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonTransitionTargetAbsent)
	}
}

func TestMalformedTransitionElementBlocks(t *testing.T) {
	// OWE vendor OUI and type followed by a truncated BSSID/SSID payload.
	malformed := []byte{221, 8, 0x50, 0x6f, 0x9a, 0x1c, 0x02, 0x00, 0x00, 0x00}
	scan := []BSS{
		{
			BSSID:    openBSSID,
			SSID:     "open-net",
			Security: SecurityOpen,
			IEs:      malformed,
		},
		{
			BSSID:    oweBSSID,
			SSID:     "owe-net",
			Security: SecurityOWE,
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonMalformedTransition {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonMalformedTransition)
	}
}

func TestMalformedTransitionAllowsExplicitFallback(t *testing.T) {
	malformed := []byte{221, 8, 0x50, 0x6f, 0x9a, 0x1c, 0x02, 0x00, 0x00, 0x00}
	scan := []BSS{
		{BSSID: openBSSID, SSID: "open-net", Security: SecurityOpen, IEs: malformed},
	}
	decision := Select(scan, Policy{AllowOpenFallback: true})
	if decision.Action != ActionConnectOpen || !decision.Fallback || decision.Reason != ReasonOpenFallback {
		t.Fatalf("explicit fallback result: %+v", decision)
	}
}

func TestRawIEsVerifiedPair(t *testing.T) {
	openIE, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: "owe-net"})
	if err != nil {
		t.Fatal(err)
	}
	oweIE, err := BuildTransitionElement(TransitionMode{BSSID: openBSSID, SSID: "open-net"})
	if err != nil {
		t.Fatal(err)
	}
	scan := []BSS{
		{
			BSSID:    openBSSID,
			SSID:     "open-net",
			Security: SecurityOpen,
			IEs:      openIE,
		},
		{
			BSSID:    oweBSSID,
			SSID:     "owe-net",
			Security: SecurityOWE,
			IEs:      oweIE,
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionConnectOWE {
		t.Fatalf("action = %s, want OWE: %+v", decision.Action, decision)
	}
	if !sameBSSID(decision.BSS.BSSID, oweBSSID) {
		t.Fatalf("selected BSS = %s, want %s", decision.BSS.BSSID, oweBSSID)
	}
}

func TestRawIEsInferSecurityModes(t *testing.T) {
	openIE, err := BuildTransitionElement(TransitionMode{BSSID: oweBSSID, SSID: "owe-net"})
	if err != nil {
		t.Fatal(err)
	}
	oweIE, err := BuildTransitionElement(TransitionMode{BSSID: openBSSID, SSID: "open-net"})
	if err != nil {
		t.Fatal(err)
	}
	scan := []BSS{
		{BSSID: openBSSID, SSID: "open-net", IEs: openIE},
		{BSSID: oweBSSID, SSID: "owe-net", IEs: append(oweIE, rsnIEWithAKM(oweAKMSuiteType)...)},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionConnectOWE {
		t.Fatalf("inferred security did not produce OWE selection: %+v", decision)
	}
}

func TestDirectOWEWithoutTransition(t *testing.T) {
	scan := []BSS{{BSSID: oweBSSID, SSID: "owe-net", Security: SecurityOWE}}
	decision := Select(scan, Policy{})
	if decision.Action != ActionConnectOWE || decision.Reason != ReasonDirectOWE {
		t.Fatalf("direct OWE result: %+v", decision)
	}
}

func TestForgedTransitionToNonOWEBSSBlocks(t *testing.T) {
	scan := []BSS{
		{
			BSSID:      openBSSID,
			SSID:       "open-net",
			Security:   SecurityOpen,
			Transition: transitionTo(otherBSSID, "other-open"),
		},
		{
			BSSID:    otherBSSID,
			SSID:     "other-open",
			Security: SecurityOpen,
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock || decision.Reason != ReasonBSSIDMismatch {
		t.Fatalf("forged non-OWE transition result: %+v", decision)
	}
}

func TestHiddenOWESSIDUsesVerifiedDeclaration(t *testing.T) {
	scan := verifiedPair("open-net", "")
	scan[0].Transition.SSID = "hidden-owe"
	scan[1].Transition = transitionTo(openBSSID, "open-net")

	decision := Select(scan, Policy{})
	if decision.Action != ActionConnectOWE {
		t.Fatalf("action = %s, want OWE: %+v", decision.Action, decision)
	}
	if decision.BSS.SSID != "hidden-owe" {
		t.Fatalf("selected SSID = %q, want declared hidden SSID", decision.BSS.SSID)
	}
	if decision.Pair == nil || decision.Pair.OWE.SSID != "hidden-owe" {
		t.Fatalf("pair = %#v, want effective hidden SSID", decision.Pair)
	}
}

func TestHiddenOWESSIDStillRequiresBidirectionalClaims(t *testing.T) {
	scan := []BSS{
		{
			BSSID:      openBSSID,
			SSID:       "open-net",
			Security:   SecurityOpen,
			Transition: transitionTo(oweBSSID, "hidden-owe"),
		},
		{
			BSSID:    oweBSSID,
			SSID:     "",
			Security: SecurityOWE,
		},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("hidden SSID bypassed reverse transition check: %+v", decision)
	}
}

func TestOWERejectionBlocksSilentOpenFallback(t *testing.T) {
	selector := NewSelector(Policy{})
	if err := selector.RejectOWE(oweBSSID); err != nil {
		t.Fatal(err)
	}
	decision := selector.Select(verifiedPair("open-net", "owe-net"))
	if decision.Action != ActionBlock {
		t.Fatalf("action = %s, want block: %+v", decision.Action, decision)
	}
	if decision.Reason != ReasonOWERejected {
		t.Fatalf("reason = %s, want %s", decision.Reason, ReasonOWERejected)
	}
}

func TestOWERejectionAllowsExplicitFallback(t *testing.T) {
	selector := NewSelector(Policy{AllowOpenFallback: true})
	if err := selector.RejectOWE(oweBSSID); err != nil {
		t.Fatal(err)
	}
	decision := selector.Select(verifiedPair("open-net", "owe-net"))
	if decision.Action != ActionConnectOpen {
		t.Fatalf("action = %s, want explicit open fallback: %+v", decision.Action, decision)
	}
	if !decision.Fallback || decision.Reason != ReasonOpenFallback {
		t.Fatalf("fallback=%t reason=%s, want explicit fallback", decision.Fallback, decision.Reason)
	}
	if decision.Pair == nil {
		t.Fatal("fallback should report the rejected pair")
	}
}

func TestRejectedPairPrefersAnotherVerifiedPair(t *testing.T) {
	secondOpen := testMAC("02:00:00:00:10:01")
	secondOWE := testMAC("02:00:00:00:10:02")
	scan := verifiedPair("open-net", "owe-net")
	scan = append(scan,
		BSS{
			BSSID:      secondOpen,
			SSID:       "open-two",
			Security:   SecurityOpen,
			RSSI:       -60,
			Transition: transitionTo(secondOWE, "owe-two"),
		},
		BSS{
			BSSID:      secondOWE,
			SSID:       "owe-two",
			Security:   SecurityOWE,
			RSSI:       -70,
			Transition: transitionTo(secondOpen, "open-two"),
		},
	)

	decision := SelectWithRejected(scan, Policy{}, oweBSSID)
	if decision.Action != ActionConnectOWE {
		t.Fatalf("action = %s, want another OWE pair: %+v", decision.Action, decision)
	}
	if !sameBSSID(decision.BSS.BSSID, secondOWE) {
		t.Fatalf("selected BSS = %s, want %s", decision.BSS.BSSID, secondOWE)
	}
}

func TestPlainOpenRequiresExplicitPolicy(t *testing.T) {
	scan := []BSS{{BSSID: openBSSID, SSID: "open-net", Security: SecurityOpen}}

	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock || decision.Reason != ReasonOpenDenied {
		t.Fatalf("default policy selected open BSS: %+v", decision)
	}

	decision = Select(scan, Policy{AllowOpenWithoutOWE: true})
	if decision.Action != ActionConnectOpen || decision.Fallback || decision.Reason != ReasonPlainOpen {
		t.Fatalf("plain-open policy result: %+v", decision)
	}
}

func TestConflictingDuplicateBSSIDBlocks(t *testing.T) {
	scan := []BSS{
		{BSSID: openBSSID, SSID: "open-net", Security: SecurityOpen,
			Transition: transitionTo(oweBSSID, "owe-net")},
		{BSSID: openBSSID, SSID: "evil-open", Security: SecurityOpen,
			Transition: transitionTo(oweBSSID, "owe-net")},
		{BSSID: oweBSSID, SSID: "owe-net", Security: SecurityOWE,
			Transition: transitionTo(openBSSID, "open-net")},
	}
	decision := Select(scan, Policy{})
	if decision.Action != ActionBlock {
		t.Fatalf("conflicting duplicate selected a BSS: %+v", decision)
	}
}

func TestSelectorConcurrentRejection(t *testing.T) {
	selector := NewSelector(Policy{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := selector.RejectOWE(oweBSSID); err != nil {
				t.Errorf("RejectOWE: %v", err)
			}
			_ = selector.Select(verifiedPair("open-net", "owe-net"))
		}()
	}
	wg.Wait()
	if decision := selector.Select(verifiedPair("open-net", "owe-net")); decision.Action != ActionBlock {
		t.Fatalf("rejection state lost under concurrency: %+v", decision)
	}

	selector.ClearRejections()
	if decision := selector.Select(verifiedPair("open-net", "owe-net")); decision.Action != ActionConnectOWE {
		t.Fatalf("cleared rejection state still blocks: %+v", decision)
	}
}
