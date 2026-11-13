package netsec_test

import (
	"testing"
	"time"

	netsec "example.org/netsec/q0770"
)

func newTestStation(pmf, associated bool) *netsec.Station {
	s := netsec.NewStation(pmf, associated)
	s.Timeout = 2 * time.Second
	s.TransactionIDSource = func() uint16 { return 0x4242 }
	return s
}

func TestPMFDiscardsUnprotectedUnicastDeauthAndDisassoc(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	for _, frameType := range []netsec.FrameType{
		netsec.FrameTypeDeauthentication,
		netsec.FrameTypeDisassociation,
	} {
		sta := newTestStation(true, true)
		sta.LastPN = 9

		got := sta.Handle(netsec.Frame{
			Type:        frameType,
			Destination: netsec.DestinationUnicast,
			Protected:   false,
			PN:          10,
		}, now)

		if !got.Dropped() {
			t.Fatalf("frame type %d: got action %d, want discard", frameType, got.Action)
		}
		if got.Reason != netsec.ReasonUnprotectedManagement {
			t.Fatalf("frame type %d: got reason %d, want unprotected-management", frameType, got.Reason)
		}
		if !sta.Association() {
			t.Fatalf("frame type %d: unprotected frame terminated the association", frameType)
		}
		if sta.LastPN != 9 {
			t.Fatalf("frame type %d: LastPN changed to %d", frameType, sta.LastPN)
		}
	}
}

func TestProtectedPNMustStrictlyIncrease(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(true, true)

	if got := sta.Evaluate(netsec.Frame{
		Type:      netsec.FrameTypeOther,
		Protected: true,
		PN:        10,
	}, now); got.Action != netsec.ActionAccept {
		t.Fatalf("first protected frame: got action %d, want accept", got.Action)
	}
	if sta.LastPN != 10 {
		t.Fatalf("LastPN = %d, want 10", sta.LastPN)
	}

	for _, replayPN := range []uint64{10, 9, 1} {
		got := sta.Evaluate(netsec.Frame{
			Type:      netsec.FrameTypeOther,
			Protected: true,
			PN:        replayPN,
		}, now)
		if !got.Dropped() || got.Reason != netsec.ReasonPNReplay {
			t.Fatalf("PN %d: got action/reason %d/%d, want discard/PN-replay", replayPN, got.Action, got.Reason)
		}
	}

	got := sta.Evaluate(netsec.Frame{
		Type:      netsec.FrameTypeDeauthentication,
		Protected: true,
		PN:        11,
	}, now)
	if got.Action != netsec.ActionAccept {
		t.Fatalf("fresh protected deauth: got action %d, want accept", got.Action)
	}
	if sta.LastPN != 11 {
		t.Fatalf("LastPN after deauth = %d, want 11", sta.LastPN)
	}
	if sta.Association() {
		t.Fatal("accepted protected deauth did not terminate the association")
	}
}

func TestInvalidPacketNumberIsDropped(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(true, true)

	for _, pn := range []uint64{0, netsec.MaxPacketNumber + 1} {
		got := sta.Evaluate(netsec.Frame{
			Type:      netsec.FrameTypeOther,
			Protected: true,
			PN:        pn,
		}, now)
		if !got.Dropped() || got.Reason != netsec.ReasonInvalidPacketNumber {
			t.Fatalf("PN %d: got action/reason %d/%d, want discard/invalid-PN", pn, got.Action, got.Reason)
		}
	}
}

func TestBroadcastRequiresBIPAndIncreasingIPN(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(true, true)

	got := sta.Evaluate(netsec.Frame{
		Type:        netsec.FrameTypeOther,
		Destination: netsec.DestinationBroadcast,
		BIPValid:    false,
		IPN:         1,
	}, now)
	if !got.Dropped() || got.Reason != netsec.ReasonBIPIntegrityFailure {
		t.Fatalf("forged broadcast: got action/reason %d/%d, want discard/BIP failure", got.Action, got.Reason)
	}
	if sta.LastIPN != 0 {
		t.Fatalf("forged broadcast changed LastIPN to %d", sta.LastIPN)
	}

	got = sta.Evaluate(netsec.Frame{
		Type:        netsec.FrameTypeOther,
		Destination: netsec.DestinationBroadcast,
		BIPValid:    true,
		IPN:         7,
	}, now)
	if got.Action != netsec.ActionAccept {
		t.Fatalf("authenticated broadcast: got action %d, want accept", got.Action)
	}
	if sta.LastIPN != 7 {
		t.Fatalf("LastIPN = %d, want 7", sta.LastIPN)
	}

	for _, replayIPN := range []uint64{7, 6} {
		got = sta.Evaluate(netsec.Frame{
			Type:        netsec.FrameTypeOther,
			Destination: netsec.DestinationBroadcast,
			BIPValid:    true,
			IPN:         replayIPN,
		}, now)
		if !got.Dropped() || got.Reason != netsec.ReasonIPNReplay {
			t.Fatalf("IPN %d: got action/reason %d/%d, want discard/IPN replay", replayIPN, got.Action, got.Reason)
		}
	}

	got = sta.Evaluate(netsec.Frame{
		Type:      netsec.FrameTypeOther,
		Broadcast: true,
		BIPValid:  false,
		IPN:       8,
	}, now)
	if !got.Dropped() || got.Reason != netsec.ReasonBIPIntegrityFailure {
		t.Fatalf("forged higher-IPN broadcast: got action/reason %d/%d", got.Action, got.Reason)
	}
	if sta.LastIPN != 7 {
		t.Fatalf("forged higher-IPN broadcast changed LastIPN to %d", sta.LastIPN)
	}
}

func TestSAQueryKeepsAssociationOnProtectedResponse(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(true, true)

	got := sta.Evaluate(netsec.Frame{
		Type:        netsec.FrameTypeAssociationRequest,
		Destination: netsec.DestinationUnicast,
		Protected:   false,
	}, now)
	if got.Action != netsec.ActionRejectTryAgainAndSAQuery {
		t.Fatalf("association request: got action %d", got.Action)
	}
	if got.StatusCode != netsec.StatusAssociationTryAgainLater {
		t.Fatalf("association request: got status %d, want 30", got.StatusCode)
	}
	if !got.SendSAQuery {
		t.Fatal("association request did not request an SA Query")
	}
	if got.TransactionID != 0x4242 {
		t.Fatalf("transaction ID = %#x, want 0x4242", got.TransactionID)
	}
	if !sta.Association() {
		t.Fatal("association request immediately terminated the association")
	}
	if pending := sta.PendingSAQuery(); pending == nil || pending.TransactionID != got.TransactionID {
		t.Fatalf("pending SA Query = %#v", pending)
	}

	got = sta.Evaluate(netsec.Frame{
		Type:          netsec.FrameTypeSAQueryResponse,
		Protected:     false,
		TransactionID: 0x4242,
	}, now.Add(100*time.Millisecond))
	if !got.Dropped() || got.Reason != netsec.ReasonSAQueryUnprotected {
		t.Fatalf("unprotected response: got action/reason %d/%d", got.Action, got.Reason)
	}
	if sta.PendingSAQuery() == nil || !sta.Association() {
		t.Fatal("unprotected response changed SA Query or association state")
	}

	got = sta.Evaluate(netsec.Frame{
		Type:          netsec.FrameTypeSAQueryResponse,
		Protected:     true,
		PN:            1,
		TransactionID: 0x1111,
	}, now.Add(200*time.Millisecond))
	if !got.Dropped() || got.Reason != netsec.ReasonSAQueryTransactionMismatch {
		t.Fatalf("wrong transaction: got action/reason %d/%d", got.Action, got.Reason)
	}
	if sta.PendingSAQuery() == nil {
		t.Fatal("wrong transaction cleared the pending SA Query")
	}

	got = sta.Evaluate(netsec.Frame{
		Type:          netsec.FrameTypeSAQueryResponse,
		Protected:     true,
		PN:            2,
		TransactionID: 0x4242,
	}, now.Add(time.Second))
	if !got.AssociationKept() {
		t.Fatalf("protected response: got action %d, want keep association", got.Action)
	}
	if !sta.Association() {
		t.Fatal("protected SA Query Response did not keep the association")
	}
	if sta.PendingSAQuery() != nil {
		t.Fatal("successful SA Query remained pending")
	}
	if sta.LastPN != 2 {
		t.Fatalf("LastPN = %d, want 2", sta.LastPN)
	}
}

func TestSAQueryTimeoutTerminatesAssociation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(true, true)

	got := sta.Handle(netsec.Frame{
		Type:        netsec.FrameTypeReassociationRequest,
		Destination: netsec.DestinationUnicast,
	}, now)
	if got.Action != netsec.ActionRejectTryAgainAndSAQuery {
		t.Fatalf("reassociation request: got action %d", got.Action)
	}
	pending := sta.PendingSAQuery()
	if pending == nil {
		t.Fatal("reassociation request did not start SA Query")
	}

	got = sta.Expire(pending.Deadline)
	if got.Action != netsec.ActionTerminateAssociation || got.Reason != netsec.ReasonSAQueryTimeout {
		t.Fatalf("timeout: got action/reason %d/%d", got.Action, got.Reason)
	}
	if sta.Association() {
		t.Fatal("SA Query timeout did not terminate the association")
	}
	if sta.PendingSAQuery() != nil {
		t.Fatal("SA Query remained pending after timeout")
	}

	lateSTA := newTestStation(true, true)
	late := lateSTA.Handle(netsec.Frame{
		Type:        netsec.FrameTypeAssociationRequest,
		Destination: netsec.DestinationUnicast,
	}, now)
	if !late.SendSAQuery {
		t.Fatal("late-response station did not start SA Query")
	}
	latePending := lateSTA.PendingSAQuery()
	if latePending == nil {
		t.Fatal("late-response station has no pending SA Query")
	}

	got = lateSTA.Evaluate(netsec.Frame{
		Type:          netsec.FrameTypeSAQueryResponse,
		Protected:     true,
		PN:            1,
		TransactionID: latePending.TransactionID,
	}, latePending.Deadline)
	if got.Action != netsec.ActionTerminateAssociation {
		t.Fatalf("late response: got action %d, want terminate", got.Action)
	}
	if lateSTA.Association() {
		t.Fatal("late protected response resurrected the association")
	}
}

func TestNonPMFStationAcceptsUnprotectedDeauth(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sta := newTestStation(false, true)

	got := sta.Evaluate(netsec.Frame{
		Type:      netsec.FrameTypeDeauthentication,
		Protected: false,
	}, now)
	if got.Action != netsec.ActionAccept {
		t.Fatalf("non-PMF deauth: got action %d, want accept", got.Action)
	}
	if sta.Association() {
		t.Fatal("accepted deauth did not terminate association")
	}
}
