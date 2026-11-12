package taskpkg

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

const (
	testClient = "10.0.0.8"
	testDomain = "evil.example"
)

func testWindow() Window {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	return Window{Start: start, End: start.Add(time.Minute)}
}

func payload(data string) []byte {
	b, err := hex.DecodeString(data)
	if err != nil {
		panic(err)
	}
	return b
}

func dnsLabel(index uint16, data string) string {
	return fmt.Sprintf("%04x%s", index, data)
}

func queryAt(w Window, id string, index uint16, data string, at time.Time) Query {
	return Query{
		QueryID:     id,
		Client:      testClient,
		Domain:      dnsLabel(index, data) + "." + testDomain,
		Timestamp:   at,
		WindowStart: w.Start,
		WindowEnd:   w.End,
	}
}

func fragmentQueries(w Window, indices []uint16, data string) []Query {
	var events []Query
	for i, index := range indices {
		events = append(events, queryAt(w, fmt.Sprintf("q-%02d", i), index, data, w.Start.Add(time.Duration(i)*time.Second)))
	}
	return events
}

func TestAlertRequiresDistinctFragmentsAndContiguousBytes(t *testing.T) {
	w := testWindow()
	d := New(4, 32)
	events := []Query{
		queryAt(w, "q-2", 2, "0202020202020202", w.Start.Add(2*time.Second)),
		queryAt(w, "q-0", 0, "0000000000000000", w.Start),
		queryAt(w, "q-3", 3, "0303030303030303", w.Start.Add(3*time.Second)),
		queryAt(w, "q-1", 1, "0101010101010101", w.Start.Add(time.Second)),
	}
	for _, event := range events {
		d.Observe(event)
	}

	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 {
		t.Fatalf("got %d alerts, want 1", len(got))
	}
	alert := got[0]
	if alert.UniqueFragments != 4 || alert.ChainFragments != 4 {
		t.Fatalf("fragments = %d/%d, want 4/4", alert.UniqueFragments, alert.ChainFragments)
	}
	if alert.ReassembledBytes != 32 {
		t.Fatalf("reassembled bytes = %d, want 32", alert.ReassembledBytes)
	}
	want := "0000000000000000" + "0101010101010101" + "0202020202020202" + "0303030303030303"
	if alert.DataHex != want {
		t.Fatalf("payload = %s, want %s", alert.DataHex, want)
	}
	if alert.FirstIndex != 0 || alert.LastIndex != 3 {
		t.Fatalf("range = %d..%d, want 0..3", alert.FirstIndex, alert.LastIndex)
	}
	if len(alert.QueryIDs) != 4 {
		t.Fatalf("query IDs = %v, want four IDs", alert.QueryIDs)
	}
}

func TestBelowThresholdAndNonContiguousRunDoNotAlert(t *testing.T) {
	w := testWindow()

	d := New(4, 32)
	for _, event := range fragmentQueries(w, []uint16{0, 1, 2}, "aabbccdd") {
		d.Observe(event)
	}
	if got := d.AdvanceWatermark(w.End); len(got) != 0 {
		t.Fatalf("three fragments alerted: %+v", got)
	}

	d = New(4, 16)
	for i, index := range []uint16{0, 2, 4, 6} {
		d.Observe(queryAt(w, fmt.Sprintf("gap-%d", i), index, "aabbccdd", w.Start.Add(time.Duration(i)*time.Second)))
	}
	if got := d.AdvanceWatermark(w.End); len(got) != 0 {
		t.Fatalf("non-contiguous fragments alerted: %+v", got)
	}
}

func TestWindowIsLeftClosedRightOpen(t *testing.T) {
	w := testWindow()
	d := New(1, 8)
	d.Observe(queryAt(w, "at-start", 0, "aabbccddeeff0011", w.Start))
	d.Observe(queryAt(w, "at-end", 1, "1122334455667788", w.End))

	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 {
		t.Fatalf("got %d alerts, want 1", len(got))
	}
	if got[0].UniqueFragments != 1 || got[0].QueryIDs[0] != "at-start" {
		t.Fatalf("unexpected alert: %+v", got[0])
	}
}

func TestRetransmissionsDoNotIncreaseDistinctCount(t *testing.T) {
	w := testWindow()
	d := New(4, 8)
	for i, index := range []uint16{0, 1, 2, 3} {
		d.Observe(queryAt(w, fmt.Sprintf("orig-%d", i), index, "01020304", w.Start.Add(time.Duration(i)*time.Second)))
	}
	d.Observe(queryAt(w, "retry-0", 0, "01020304", w.Start.Add(10*time.Second)))
	d.Observe(queryAt(w, "retry-2", 2, "01020304", w.Start.Add(11*time.Second)))

	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 {
		t.Fatalf("got %d alerts, want 1", len(got))
	}
	if got[0].UniqueFragments != 4 || got[0].ChainFragments != 4 {
		t.Fatalf("fragments = %d/%d, want 4/4", got[0].UniqueFragments, got[0].ChainFragments)
	}
	if got[0].ReassembledBytes != 16 {
		t.Fatalf("bytes = %d, want 16", got[0].ReassembledBytes)
	}
	if got[0].Duplicates != 2 {
		t.Fatalf("duplicates = %d, want 2", got[0].Duplicates)
	}
}

func TestConflictingContentIsReportedSeparately(t *testing.T) {
	w := testWindow()
	d := New(2, 8)
	d.Observe(queryAt(w, "q-0-first", 0, "aaaaaaaaaaaaaaaa", w.Start))
	d.Observe(queryAt(w, "q-1", 1, "bbbbbbbbbbbbbbbb", w.Start.Add(time.Second)))
	d.Observe(queryAt(w, "q-0-conflict", 0, "cccccccccccccccc", w.Start.Add(2*time.Second)))

	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 {
		t.Fatalf("got %d alerts, want 1", len(got))
	}
	alert := got[0]
	if alert.UniqueFragments != 2 {
		t.Fatalf("unique fragments = %d, want 2", alert.UniqueFragments)
	}
	if alert.DataHex != "aaaaaaaaaaaaaaaabbbbbbbbbbbbbbbb" {
		t.Fatalf("canonical payload = %s", alert.DataHex)
	}
	if len(alert.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one", alert.Conflicts)
	}
	conflict := alert.Conflicts[0]
	if conflict.Index != 0 || conflict.DataHex != "cccccccccccccccc" {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	if conflict.QueryID != "q-0-conflict" || conflict.CanonicalQueryID != "q-0-first" {
		t.Fatalf("unexpected conflict IDs: %+v", conflict)
	}
	if len(alert.AllFragments) != 2 {
		t.Fatalf("all fragments = %+v, want two", alert.AllFragments)
	}
}

func TestOrdinaryNonHexDomainsAreIgnored(t *testing.T) {
	w := testWindow()
	d := New(1, 1)
	for i, domain := range []string{
		"www.evil.example",
		"api-v1.evil.example",
		"some-really-long-but-legitimate-label-for-a-service.evil.example",
		"service-2026-11-12.evil.example",
	} {
		d.Observe(Query{
			QueryID:     fmt.Sprintf("plain-%d", i),
			Client:      testClient,
			Domain:      domain,
			Timestamp:   w.Start.Add(time.Duration(i) * time.Second),
			WindowStart: w.Start,
			WindowEnd:   w.End,
		})
	}
	if got := d.AdvanceWatermark(w.End); len(got) != 0 {
		t.Fatalf("non-hex domains alerted: %+v", got)
	}
}

func TestWatermarkSealsWindowsAndDropsLateEvents(t *testing.T) {
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	w1 := Window{Start: base, End: base.Add(10 * time.Second)}
	w2 := Window{Start: w1.End, End: base.Add(20 * time.Second)}

	d := New(1, 4)
	d.Observe(queryAt(w1, "w1-q", 0, "aabbccdd", base.Add(time.Second)))
	d.Observe(queryAt(w2, "w2-q", 0, "11223344", w2.Start.Add(time.Second)))

	first := d.AdvanceWatermark(w1.End)
	if len(first) != 1 || first[0].Window != w1 || first[0].QueryIDs[0] != "w1-q" {
		t.Fatalf("first seal = %+v", first)
	}

	// The first window is complete; a packet arriving now for it is late and
	// must not reopen or alter it.
	d.Observe(queryAt(w1, "late-w1-q", 1, "deadbeef", base.Add(5*time.Second)))

	second := d.AdvanceWatermark(w2.End)
	if len(second) != 1 || second[0].Window != w2 || second[0].QueryIDs[0] != "w2-q" {
		t.Fatalf("second seal = %+v", second)
	}
	if second[0].UniqueFragments != 1 {
		t.Fatalf("late event reopened sealed window: %+v", second[0])
	}
}

func TestConflictsAndRetransmissionsAreOrderIndependent(t *testing.T) {
	w := testWindow()
	events := []Query{
		queryAt(w, "q-0-a", 0, "aaaaaaaaaaaaaaaa", w.Start),
		queryAt(w, "q-0-a-retry", 0, "aaaaaaaaaaaaaaaa", w.Start.Add(time.Second)),
		queryAt(w, "q-0-b", 0, "bbbbbbbbbbbbbbbb", w.Start.Add(2*time.Second)),
		queryAt(w, "q-1", 1, "cccccccccccccccc", w.Start.Add(3*time.Second)),
		queryAt(w, "q-2", 2, "dddddddddddddddd", w.Start.Add(4*time.Second)),
		queryAt(w, "q-3", 3, "eeeeeeeeeeeeeeee", w.Start.Add(5*time.Second)),
	}

	build := func(perm []int) Alert {
		d := New(4, 32)
		for _, i := range perm {
			d.Observe(events[i])
		}
		got := d.AdvanceWatermark(w.End)
		if len(got) != 1 {
			t.Fatalf("permutation %v got %d alerts", perm, len(got))
		}
		return got[0]
	}

	inOrder := build([]int{0, 1, 2, 3, 4, 5})
	reversed := build([]int{5, 4, 3, 2, 0, 1})
	if inOrder.DataHex != reversed.DataHex {
		t.Fatalf("payload depends on arrival order: %s vs %s", inOrder.DataHex, reversed.DataHex)
	}
	if inOrder.UniqueFragments != reversed.UniqueFragments || len(inOrder.Conflicts) != len(reversed.Conflicts) {
		t.Fatalf("counts depend on arrival order: %+v vs %+v", inOrder, reversed)
	}
	if inOrder.Conflicts[0].QueryID != reversed.Conflicts[0].QueryID {
		t.Fatalf("conflict evidence depends on arrival order: %+v vs %+v", inOrder.Conflicts[0], reversed.Conflicts[0])
	}
}

func TestEvaluateMatchesStreamingUnderReordering(t *testing.T) {
	w := testWindow()
	var events []Query
	for i := uint16(0); i < 8; i++ {
		data := fmt.Sprintf("%02x%02x%02x%02x", i, i+1, i+2, i+3)
		events = append(events, queryAt(w, fmt.Sprintf("ev-%02d", i), i, data, w.Start.Add(time.Duration(i)*time.Second)))
	}
	// Add a retransmission, a conflicting variant and a non-hex event.
	events = append(events,
		queryAt(w, "ev-03-retry", 3, "03040506", w.Start.Add(20*time.Second)),
		queryAt(w, "ev-05-conflict", 5, "ffffffff", w.Start.Add(21*time.Second)),
		Query{
			QueryID:     "plain",
			Client:      testClient,
			Domain:      "www.evil.example",
			Timestamp:   w.Start.Add(22 * time.Second),
			WindowStart: w.Start,
			WindowEnd:   w.End,
		},
	)

	streaming := func(order []int) Alert {
		d := New(4, 24)
		for _, i := range order {
			d.Observe(events[i])
		}
		got := d.AdvanceWatermark(w.End)
		if len(got) != 1 {
			t.Fatalf("streaming got %d alerts", len(got))
		}
		return got[0]
	}

	orderA := make([]int, len(events))
	for i := range orderA {
		orderA[i] = i
	}
	orderB := append([]int(nil), orderA...)
	sort.Slice(orderB, func(i, j int) bool { return events[orderB[i]].QueryID > events[orderB[j]].QueryID })

	a := streaming(orderA)
	b := streaming(orderB)
	batch := Evaluate(events, w, Config{K: 4, B: 24})
	if len(batch) != 1 {
		t.Fatalf("batch got %d alerts", len(batch))
	}

	for name, got := range map[string]Alert{"reordered": b, "batch": batch[0]} {
		if !reflect.DeepEqual(got, a) {
			t.Fatalf("%s result differs from streaming recalculation\ngot:  %+v\nwant: %+v", name, got, a)
		}
	}
}

func TestRandomizedOrderingMatchesFullRecompute(t *testing.T) {
	w := testWindow()
	var base []Query
	for i := uint16(0); i < 6; i++ {
		data := fmt.Sprintf("%02x%02x%02x%02x", i, i+1, i+2, i+3)
		base = append(base, queryAt(w, fmt.Sprintf("base-%02d", i), i, data, w.Start.Add(time.Duration(i)*time.Second)))
	}
	base = append(base,
		queryAt(w, "retry-02", 2, "02030405", w.Start.Add(30*time.Second)),
		queryAt(w, "conflict-03", 3, "ffffffff", w.Start.Add(31*time.Second)),
		Query{
			QueryID:     "legit-long-label",
			Client:      testClient,
			Domain:      "this-is-a-long-legitimate-label-that-is-not-hex.evil.example",
			Timestamp:   w.Start.Add(32 * time.Second),
			WindowStart: w.Start,
			WindowEnd:   w.End,
		},
	)

	want := Evaluate(base, w, Config{K: 4, B: 12})
	if len(want) != 1 {
		t.Fatalf("full recomputation got %d alerts, want 1", len(want))
	}

	rng := rand.New(rand.NewSource(42))
	for round := 0; round < 50; round++ {
		perm := rng.Perm(len(base))
		d := New(4, 12)
		for _, i := range perm {
			d.Observe(base[i])
		}
		got := d.AdvanceWatermark(w.End)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d permutation %v differs\ngot:  %+v\nwant: %+v", round, perm, got, want)
		}
	}
}

func TestGroupsClientsAndRegisteredDomainsSeparately(t *testing.T) {
	w := testWindow()
	d := New(1, 4)
	d.Observe(Query{
		QueryID: "c1-d1", Client: "c1", Domain: dnsLabel(0, "aabbccdd") + ".one.example",
		Timestamp: w.Start, WindowStart: w.Start, WindowEnd: w.End,
	})
	d.Observe(Query{
		QueryID: "c1-d2", Client: "c1", Domain: dnsLabel(1, "11223344") + ".two.example",
		Timestamp: w.Start.Add(time.Second), WindowStart: w.Start, WindowEnd: w.End,
	})
	d.Observe(Query{
		QueryID: "c2-d1", Client: "c2", Domain: dnsLabel(2, "deadbeef") + ".one.example",
		Timestamp: w.Start.Add(2 * time.Second), WindowStart: w.Start, WindowEnd: w.End,
	})

	got := d.AdvanceWatermark(w.End)
	if len(got) != 3 {
		t.Fatalf("got %d alerts, want 3", len(got))
	}
	if got[0].Client != "c1" || got[0].RegisteredDomain != "one.example" {
		t.Fatalf("alerts are not sorted/grouped as expected: %+v", got)
	}
}

func TestRegisteredDomainSelectsLabelBeforeIt(t *testing.T) {
	w := testWindow()
	d := New(1, 2)
	q := Query{
		QueryID:          "nested",
		Client:           testClient,
		Domain:           "noise." + dnsLabel(7, "aabb") + ".evil.example",
		RegisteredDomain: "evil.example",
		Timestamp:        w.Start,
		WindowStart:      w.Start,
		WindowEnd:        w.End,
	}
	d.Observe(q)
	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 {
		t.Fatalf("got %d alerts, want 1", len(got))
	}
	if got[0].Fragments[0].Index != 7 || got[0].ReassembledBytes != 2 {
		t.Fatalf("wrong label selected: %+v", got[0].Fragments)
	}
}

func TestDomainMayBeSuppliedAsPayloadLabel(t *testing.T) {
	w := testWindow()
	d := New(1, 2)
	d.Observe(Query{
		QueryID:          "label-only",
		Client:           testClient,
		Domain:           dnsLabel(4, "aabb"),
		RegisteredDomain: testDomain,
		Timestamp:        w.Start,
		WindowStart:      w.Start,
		WindowEnd:        w.End,
	})
	got := d.AdvanceWatermark(w.End)
	if len(got) != 1 || got[0].RegisteredDomain != testDomain {
		t.Fatalf("label-only domain was not accepted: %+v", got)
	}
}

func TestParseHexLabel(t *testing.T) {
	index, data, ok := ParseHexLabel("00AFDEADBEEF")
	if !ok || index != 0x00af || hex.EncodeToString(data) != "deadbeef" {
		t.Fatalf("parse = %d %x %v", index, data, ok)
	}
	if _, _, ok := ParseHexLabel("0001dead"); !ok {
		t.Fatal("valid label rejected")
	}
	for _, bad := range []string{"", "abc", "000g", "0001abc", "payload"} {
		if _, _, ok := ParseHexLabel(bad); ok {
			t.Fatalf("invalid label accepted: %q", bad)
		}
	}
}

func TestSnapshotReportsConflictsWithoutSealing(t *testing.T) {
	w := testWindow()
	d := New(2, 8)
	d.Observe(queryAt(w, "a", 0, "aaaaaaaaaaaaaaaa", w.Start))
	d.Observe(queryAt(w, "b", 1, "bbbbbbbbbbbbbbbb", w.Start.Add(time.Second)))
	d.Observe(queryAt(w, "c", 0, "cccccccccccccccc", w.Start.Add(2*time.Second)))

	reports := d.Snapshot()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	if len(reports[0].Conflicts) != 1 || !reports[0].Detected {
		t.Fatalf("unexpected report: %+v", reports[0])
	}
	if got := d.AdvanceWatermark(w.End); len(got) != 1 {
		t.Fatalf("snapshot sealed the window")
	}
}
