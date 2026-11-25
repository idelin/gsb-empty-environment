package reuseauth

import (
	"testing"
)

type differentialRun struct {
	outsiderJSON []byte
	ownerJSON    []byte
	outsiderHash Hash
	ownerHash    Hash
}

func runHiddenDifferential(t *testing.T, hidden []byte) differentialRun {
	t.Helper()
	f := newFixture(t)
	app := f.origins["app"]
	partner := f.origins["partner"]
	cdn := f.origins["cdn"]

	publicJourney := f.evidence(evidenceConfig{
		data:        []byte("PUBLIC01"),
		public:      true,
		knownLength: true,
	})
	privateJourney := f.evidence(evidenceConfig{
		data:        hidden,
		public:      false,
		audience:    app,
		knownLength: true,
	})

	owner := newTestNav(t, "nav-owner", app, CredentialsInclude)
	allowCDNAnything(t, owner, cdn, VisibilityPrivate)
	outsider := newTestNav(t, "nav-outsider", partner, CredentialsOmit)
	allowCDNAnything(t, outsider, cdn, VisibilityPublic)

	broker := NewBroker(f.auth)
	for _, nav := range []*Navigation{owner, outsider} {
		if err := broker.RegisterNavigation(nav); err != nil {
			t.Fatal(err)
		}
	}
	for id, journey := range map[DownloadID]journey{
		"dl-public":  publicJourney,
		"dl-private": privateJourney,
	} {
		if err := broker.PublishObject(PublishedObject{ID: id, Chain: journey.Chain, Response: journey.Response, Manifest: journey.Manifest}, 10); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := broker.Wait(outsider.ID, "dl-public", publicJourney.request("req-public", partner, nil)); err != nil {
		t.Fatal(err)
	}
	broker.Deliver(Callback{DownloadID: "dl-public", Kind: CallbackComplete, Public: true, At: 11})

	if _, err := broker.Wait(owner.ID, "dl-private", privateJourney.request("req-private", app, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Wait(outsider.ID, "dl-private", privateJourney.request("req-private-probe", partner, nil)); err == nil {
		t.Fatal("outsider unexpectedly reused private download")
	}

	outsiderJSON, err := broker.ChangesJSON(outsider.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownerJSON, err := broker.ChangesJSON(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return differentialRun{
		outsiderJSON: outsiderJSON,
		ownerJSON:    ownerJSON,
		outsiderHash: broker.TraceHash(outsider.ID),
		ownerHash:    broker.TraceHash(owner.ID),
	}
}

func TestHiddenDataDifferentialVisibleTraces(t *testing.T) {
	first := runHiddenDifferential(t, []byte("HIDDENAA"))
	second := runHiddenDifferential(t, []byte("HIDDENBB"))

	if string(first.outsiderJSON) != string(second.outsiderJSON) {
		t.Fatalf("outsider traces differ when only hidden bytes changed:\nfirst=%s\nsecond=%s", first.outsiderJSON, second.outsiderJSON)
	}
	if first.outsiderHash != second.outsiderHash {
		t.Fatal("outsider trace hashes differ")
	}
	if first.ownerHash == second.ownerHash || string(first.ownerJSON) == string(second.ownerJSON) {
		t.Fatal("owner trace did not change when authorized hidden representation changed")
	}
}
