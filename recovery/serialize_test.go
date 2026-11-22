package recovery

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBundleJSONRoundTrip(t *testing.T) {
	anchor, dirPriv, witPriv := newTestTrust(t)
	dev := makeDevice(t, "acct-1", "device-A", "Alice Phone",
		[]Hash{domainHash("c0"), domainHash("c1")})
	b := newBranch(dirPriv)
	b.appendEvent(dev.addEvent(EventAdd))
	send := b.head()
	req := buildRequest(dev, dev.commitment(0, 0), send, []byte("wire-key"))

	a := NewAcceptor(anchor, nil)
	b.ingestChain(a)
	ingestDeviceChainAt(t, a, b, dev.device, int(send.TreeSize))
	ingestPrekeyProof(t, a, dev, 0, 0)
	w := newWitness(witPriv)
	ingestReceiptBundle(t, a, w, req, makeReceipt(t, dev, req))
	decision := a.Evaluate(req)
	if decision.Status != StatusAccepted {
		t.Fatalf("setup: %s/%s", decision.Status, decision.Reason)
	}
	bundle, err := a.ExportBundle(req, decision)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Bundle
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Records) != len(bundle.Records) {
		t.Fatalf("record count changed after round trip: %d vs %d",
			len(decoded.Records), len(bundle.Records))
	}
	again, err := VerifyBundle(decoded)
	if err != nil || again.Status != StatusAccepted {
		t.Fatalf("decoded bundle must verify: %v %+v", err, again)
	}
	if !bytes.Equal(again.Request.WrappedRecoveryKey, []byte("wire-key")) {
		t.Fatalf("wrapped recovery key not preserved")
	}
}
