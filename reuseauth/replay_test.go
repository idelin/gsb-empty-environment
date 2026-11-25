package reuseauth

import (
	"strings"
	"testing"
)

func assertRangeStrings(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ranges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range %d = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFullReplayMultiSegmentRedirects(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]
	journey := f.evidence(evidenceConfig{
		data:        []byte("abcdefgh"),
		public:      true,
		knownLength: true,
	})
	if len(journey.Chain.Hops) != 3 {
		t.Fatalf("hops = %d, want 3 signed redirect/terminal hops", len(journey.Chain.Hops))
	}

	nav := newTestNav(t, "nav-public", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-public", Chain: journey.Chain, Response: journey.Response, Manifest: journey.Manifest}, 10); err != nil {
		t.Fatal(err)
	}

	cs, err := broker.Wait(nav.ID, "dl-public", journey.request("req-multi", app, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertRangeStrings(t, changeRangeStrings(cs), "[0,2)", "[2,4)", "[4,6)", "[6,8)")

	receipts := broker.Deliver(Callback{DownloadID: "dl-public", Kind: CallbackComplete, Public: true, At: 11})
	if len(receipts) != 1 || !receipts[0].Delivered {
		t.Fatalf("completion receipts = %+v, want one delivered", receipts)
	}

	changes := broker.Changes(nav.ID)
	if len(changes) != 2 || !changes[1].Completed {
		t.Fatalf("changes = %+v, want range add then completion", changes)
	}
	raw, err := broker.ChangesJSON(nav.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "dl-public") {
		t.Fatalf("navigation trace leaks DownloadID: %s", raw)
	}
	if drained := broker.Drain(nav.ID); len(drained) != 2 {
		t.Fatalf("drained changes = %d, want 2", len(drained))
	}
	if drained := broker.Drain(nav.ID); len(drained) != 0 {
		t.Fatalf("second drain = %d, want 0", len(drained))
	}
	if replayed := broker.Changes(nav.ID); len(replayed) != 2 {
		t.Fatalf("full replay after drain = %d, want 2", len(replayed))
	}
}

func TestFullReplayPartialRepresentationChange(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]

	cached := []byte("abcdefgh")
	changed := []byte("abXXefgh")
	journey := f.evidence(evidenceConfig{data: cached, public: true, knownLength: true})
	offered := manifestForData(changed, 2).RangeDigests()

	nav := newTestNav(t, "nav-partial", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-partial", Chain: journey.Chain, Response: journey.Response, Manifest: journey.Manifest}, 10); err != nil {
		t.Fatal(err)
	}

	cs, err := broker.Wait(nav.ID, "dl-partial", journey.request("req-partial", app, offered))
	if err != nil {
		t.Fatal(err)
	}
	assertRangeStrings(t, changeRangeStrings(cs), "[0,2)", "[4,6)", "[6,8)")

	view, ok := broker.View(nav.ID, "req-partial")
	if !ok || len(view.Ranges) != 3 {
		t.Fatalf("view = %+v ok=%v", view, ok)
	}
	for _, rd := range view.Ranges {
		if rd.Range.String() == "[2,4)" {
			t.Fatalf("changed middle range was shared: %+v", rd)
		}
	}
}

func TestEquivalentRangesNeverInfersSubrange(t *testing.T) {
	data := []byte("abcdefgh")
	full, err := NewRange(0, 8)
	if err != nil {
		t.Fatal(err)
	}
	fullDigest, err := MakeRangeDigest(data, full)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := NewRange(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	prefixDigest, err := MakeRangeDigest(data, prefix)
	if err != nil {
		t.Fatal(err)
	}

	shared, err := EquivalentRanges([]RangeDigest{fullDigest}, []RangeDigest{prefixDigest})
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 0 {
		t.Fatalf("shared %v despite no exact range-bound witness", shared)
	}
}

func TestDiffRangesMinimalReplacement(t *testing.T) {
	before := []RangeDigest{
		{Range: MustRange(0, 2), Digest: Hash{1}},
		{Range: MustRange(2, 4), Digest: Hash{2}},
	}
	after := []RangeDigest{
		{Range: MustRange(0, 2), Digest: Hash{1}},
		{Range: MustRange(2, 4), Digest: Hash{9}},
		{Range: MustRange(4, 6), Digest: Hash{3}},
	}
	added, removed := DiffRanges(before, after)
	assertRangeStrings(t, rangeDigestStrings(added), "[2,4)", "[4,6)")
	if len(removed) != 1 || removed[0].String() != "[2,4)" {
		t.Fatalf("removed = %v, want [2,4)", removed)
	}
}

func rangeDigestStrings(ranges []RangeDigest) []string {
	out := make([]string, 0, len(ranges))
	for _, rd := range ranges {
		out = append(out, rd.Range.String())
	}
	return out
}
