package netsec

import (
	"reflect"
	"testing"
)

func testSOA(name string, serial uint32) Record {
	return NewSOA(name, serial)
}

func testRR(name string, typ uint16, data string) Record {
	return NewRR(name, typ, 0, data)
}

func testChunk(old, newer Record, deleted, added []Record) []Record {
	out := make([]Record, 0, 2+len(deleted)+len(added))
	out = append(out, old.Clone())
	out = append(out, cloneRecords(deleted)...)
	out = append(out, newer.Clone())
	out = append(out, cloneRecords(added)...)
	return out
}

func testResponse(head Record, chunks ...[]Record) []Record {
	out := make([]Record, 0, 2)
	out = append(out, head.Clone())
	for _, chunk := range chunks {
		out = append(out, cloneRecords(chunk)...)
	}
	out = append(out, head.Clone())
	return out
}

func TestAdmitIXFRUpToDateAndDiscard(t *testing.T) {
	const zoneName = "example.com."

	t.Run("equal serial is up to date", func(t *testing.T) {
		head := testSOA(zoneName, 100)
		decision := AdmitIXFR(100, []Record{head})
		if decision.Status != IXFRUpToDate {
			t.Fatalf("status = %s, want up-to-date (reason: %s)", decision.Status, decision.Reason())
		}
		if len(decision.Deltas) != 0 {
			t.Fatalf("up-to-date response produced %d deltas", len(decision.Deltas))
		}
	})

	t.Run("older serial is discarded", func(t *testing.T) {
		head := testSOA(zoneName, 99)
		decision := AdmitIXFR(100, []Record{head})
		if decision.Status != IXFRDiscard {
			t.Fatalf("status = %s, want discard (reason: %s)", decision.Status, decision.Reason())
		}
		if decision.RequiresAXFR() {
			t.Fatalf("older serial incorrectly requested AXFR fallback")
		}
	})

	t.Run("serial half range apart is discarded", func(t *testing.T) {
		head := testSOA(zoneName, 100+1<<31)
		decision := AdmitIXFR(100, []Record{head})
		if decision.Status != IXFRDiscard {
			t.Fatalf("status = %s, want discard for undefined comparison (reason: %s)", decision.Status, decision.Reason())
		}
		if decision.RequiresAXFR() {
			t.Fatalf("undefined comparison incorrectly requested AXFR fallback")
		}
	})

	t.Run("newer serial with no change data falls back", func(t *testing.T) {
		head := testSOA(zoneName, 101)
		decision := AdmitIXFR(100, []Record{head})
		if decision.Status != IXFRFallbackAXFR {
			t.Fatalf("status = %s, want AXFR fallback (reason: %s)", decision.Status, decision.Reason())
		}
	})
}

func TestAdmitIXFRValidSingleChunk(t *testing.T) {
	const zoneName = "example.com."
	old := testSOA(zoneName, 10)
	newer := testSOA(zoneName, 12)
	deleted := testRR(zoneName, TypeA, "192.0.2.1")
	added := testRR(zoneName, TypeA, "192.0.2.20")
	response := testResponse(newer, testChunk(old, newer, []Record{deleted}, []Record{added}))

	decision := AdmitIXFR(10, response)
	if decision.Status != IXFRApply {
		t.Fatalf("status = %s, want apply (reason: %s)", decision.Status, decision.Reason())
	}
	if len(decision.Deltas) != 1 {
		t.Fatalf("deltas = %d, want 1", len(decision.Deltas))
	}
	if len(decision.Deltas[0].Deleted) != 1 || len(decision.Deltas[0].Added) != 1 {
		t.Fatalf("delta contents = %d deleted, %d added; want 1 and 1",
			len(decision.Deltas[0].Deleted), len(decision.Deltas[0].Added))
	}

	zone := Zone{
		Name: zoneName,
		Records: []Record{
			old,
			deleted,
			testRR(zoneName, TypeTXT, "keep"),
		},
	}
	finalDecision, err := zone.ApplyIXFR(response)
	if err != nil {
		t.Fatalf("ApplyIXFR() error = %v", err)
	}
	if finalDecision.Status != IXFRApply {
		t.Fatalf("ApplyIXFR() status = %s, want apply", finalDecision.Status)
	}
	if serial, ok := zone.Serial(); !ok || serial != 12 {
		t.Fatalf("zone serial = %d (ok=%v), want 12", serial, ok)
	}
	if _, ok := zone.SOA(); !ok {
		t.Fatalf("zone lost its SOA")
	}
	if findRecord(zone.Records, deleted) >= 0 {
		t.Fatalf("deleted A record is still present")
	}
	if findRecord(zone.Records, added) < 0 {
		t.Fatalf("added A record is missing")
	}
	if findRecord(zone.Records, testRR(zoneName, TypeTXT, "keep")) < 0 {
		t.Fatalf("unrelated TXT record was lost")
	}
}

func TestApplyIXFRValidMultipleChunksAcrossWrap(t *testing.T) {
	const zoneName = "example.com."
	old := testSOA(zoneName, 0xfffffff0)
	mid := testSOA(zoneName, 0xffffffff)
	current := testSOA(zoneName, 5)

	aOld := testRR(zoneName, TypeA, "192.0.2.1")
	bNew := testRR(zoneName, TypeA, "192.0.2.2")
	cNew := testRR(zoneName, TypeA, "192.0.2.3")
	keep := testRR(zoneName, TypeTXT, "keep")

	response := testResponse(current,
		testChunk(old, mid, []Record{aOld}, []Record{bNew}),
		testChunk(mid, current, []Record{bNew}, []Record{cNew}),
	)

	decision := AdmitIXFR(0xfffffff0, response)
	if decision.Status != IXFRApply {
		t.Fatalf("status = %s, want apply (reason: %s)", decision.Status, decision.Reason())
	}
	if len(decision.Deltas) != 2 {
		t.Fatalf("deltas = %d, want 2", len(decision.Deltas))
	}

	zone := Zone{Name: zoneName, Records: []Record{old, aOld, keep}}
	finalDecision, err := zone.ApplyIXFR(response)
	if err != nil {
		t.Fatalf("ApplyIXFR() error = %v", err)
	}
	if finalDecision.Status != IXFRApply {
		t.Fatalf("ApplyIXFR() status = %s, want apply", finalDecision.Status)
	}
	if serial, ok := zone.Serial(); !ok || serial != 5 {
		t.Fatalf("zone serial = %d (ok=%v), want 5", serial, ok)
	}
	if findRecord(zone.Records, aOld) >= 0 || findRecord(zone.Records, bNew) >= 0 {
		t.Fatalf("records deleted by one of the chunks are still present")
	}
	if findRecord(zone.Records, cNew) < 0 {
		t.Fatalf("record added by the final chunk is missing")
	}
	if findRecord(zone.Records, keep) < 0 {
		t.Fatalf("unrelated TXT record was lost")
	}
}

func TestAdmitIXFRImplicitFinalSOA(t *testing.T) {
	const zoneName = "example.com."
	old := testSOA(zoneName, 10)
	current := testSOA(zoneName, 12)
	added := testRR(zoneName, TypeTXT, "added")

	// A single chunk where the newer SOA is also the final SOA; no separate
	// trailing sentinel copy.
	response := []Record{
		current,
		old,
		current,
		added,
	}

	decision := AdmitIXFR(10, response)
	if decision.Status != IXFRApply {
		t.Fatalf("status = %s, want apply (reason: %s)", decision.Status, decision.Reason())
	}
	if len(decision.Deltas) != 1 || len(decision.Deltas[0].Added) != 1 {
		t.Fatalf("unexpected delta shape: %+v", decision.Deltas)
	}
}

func TestAdmitIXFRBrokenResponsesFallback(t *testing.T) {
	const zoneName = "example.com."
	local := uint32(10)
	head := testSOA(zoneName, 30)

	validFirst := testChunk(
		testSOA(zoneName, 10),
		testSOA(zoneName, 20),
		[]Record{testRR(zoneName, TypeA, "192.0.2.1")},
		[]Record{testRR(zoneName, TypeA, "192.0.2.2")},
	)

	tests := []struct {
		name     string
		response []Record
	}{
		{
			name:     "empty response",
			response: nil,
		},
		{
			name:     "first record is not SOA",
			response: []Record{testRR(zoneName, TypeA, "192.0.2.1"), head, head},
		},
		{
			name:     "truncated single chunk",
			response: []Record{head, testSOA(zoneName, 10)},
		},
		{
			name: "final SOA does not match first SOA",
			response: append(
				[]Record{head},
				append(validFirst, testSOA(zoneName, 31))...,
			),
		},
		{
			name: "final SOA has same serial but different RDATA",
			response: []Record{
				testSOAWithTTL(zoneName, 30, 3600),
				testSOAWithTTL(zoneName, 10, 3600),
				testSOAWithTTL(zoneName, 30, 3600),
				testSOAWithTTL(zoneName, 30, 7200),
			},
		},
		{
			name: "first chunk does not start at local serial",
			response: testResponse(head, testChunk(
				testSOA(zoneName, 11),
				testSOA(zoneName, 30),
				nil,
				nil,
			)),
		},
		{
			name: "chain gap between chunks",
			response: testResponse(head,
				testChunk(testSOA(zoneName, 10), testSOA(zoneName, 20), nil, nil),
				testChunk(testSOA(zoneName, 25), testSOA(zoneName, 30), nil, nil),
			),
		},
		{
			name: "non-monotonic chunk",
			response: testResponse(head, testChunk(
				testSOA(zoneName, 10),
				testSOA(zoneName, 5),
				nil,
				nil,
			)),
		},
		{
			name: "last chunk does not end at current serial",
			response: testResponse(head, testChunk(
				testSOA(zoneName, 10),
				testSOA(zoneName, 20),
				nil,
				nil,
			)),
		},
		{
			name: "current SOA is not the final SOA record",
			response: []Record{
				head,
				testSOA(zoneName, 10),
				testSOA(zoneName, 30),
				head,
				testRR(zoneName, TypeTXT, "trailing"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := AdmitIXFR(local, test.response)
			if decision.Status != IXFRFallbackAXFR {
				t.Fatalf("status = %s, want AXFR fallback (reason: %s)", decision.Status, decision.Reason())
			}
			if !decision.RequiresAXFR() {
				t.Fatalf("RequiresAXFR() = false for fallback decision")
			}
		})
	}
}

func testSOAWithTTL(name string, serial uint32, ttl uint32) Record {
	return NewSOAWith(name, ttl, SOA{Serial: serial})
}

func TestApplyIXFRBrokenChainLeavesZoneUntouched(t *testing.T) {
	const zoneName = "example.com."
	local := testSOA(zoneName, 1)
	aOld := testRR(zoneName, TypeA, "192.0.2.1")
	bOld := testRR(zoneName, TypeTXT, "old")
	zone := Zone{Name: zoneName, Records: []Record{local, aOld, bOld}}
	before := zone.Clone()

	head := testSOA(zoneName, 3)
	response := testResponse(head,
		testChunk(testSOA(zoneName, 1), testSOA(zoneName, 2),
			[]Record{aOld}, []Record{testRR(zoneName, TypeA, "192.0.2.2")}),
		// Serial 4 creates a gap from the previous chunk's serial 2.
		testChunk(testSOA(zoneName, 4), testSOA(zoneName, 3),
			[]Record{bOld}, []Record{testRR(zoneName, TypeTXT, "new")}),
	)

	decision, err := zone.ApplyIXFR(response)
	if err == nil {
		t.Fatalf("ApplyIXFR() accepted a broken chain")
	}
	if decision.Status != IXFRFallbackAXFR {
		t.Fatalf("status = %s, want AXFR fallback", decision.Status)
	}
	if !reflect.DeepEqual(zone, before) {
		t.Fatalf("zone was partially modified after a broken chain:\nbefore=%+v\nafter=%+v", before, zone)
	}
}

func TestApplyIXFRInvalidDeletionLeavesZoneUntouched(t *testing.T) {
	const zoneName = "example.com."
	local := testSOA(zoneName, 1)
	aOld := testRR(zoneName, TypeA, "192.0.2.1")
	bOld := testRR(zoneName, TypeTXT, "old")
	zone := Zone{Name: zoneName, Records: []Record{local, aOld, bOld}}
	before := zone.Clone()

	missing := testRR(zoneName, TypeTXT, "never-existed")
	head := testSOA(zoneName, 3)
	response := testResponse(head,
		testChunk(testSOA(zoneName, 1), testSOA(zoneName, 2),
			[]Record{aOld}, []Record{testRR(zoneName, TypeA, "192.0.2.2")}),
		// Structurally valid chain, but the second chunk deletes a record
		// that is not in the local zone. The zero-copy application must fail
		// before committing anything.
		testChunk(testSOA(zoneName, 2), testSOA(zoneName, 3),
			[]Record{missing}, []Record{testRR(zoneName, TypeTXT, "new")}),
	)

	decision, err := zone.ApplyIXFR(response)
	if err == nil {
		t.Fatalf("ApplyIXFR() accepted a delta deleting a missing record")
	}
	if decision.Status != IXFRFallbackAXFR {
		t.Fatalf("status = %s, want AXFR fallback", decision.Status)
	}
	if !reflect.DeepEqual(zone, before) {
		t.Fatalf("zone was partially modified after an invalid deletion:\nbefore=%+v\nafter=%+v", before, zone)
	}
}

func TestApplyIXFRDiscardAndUpToDateDoNotMutate(t *testing.T) {
	const zoneName = "example.com."
	local := testSOA(zoneName, 100)
	zone := Zone{Name: zoneName, Records: []Record{
		local,
		testRR(zoneName, TypeA, "192.0.2.1"),
	}}
	before := zone.Clone()

	for _, response := range [][]Record{
		{testSOA(zoneName, 99)},
		{testSOA(zoneName, 100), testRR(zoneName, TypeCNAME, "forged.example.net.")},
		{testSOA(zoneName, 100+1<<31)},
	} {
		decision, err := zone.ApplyIXFR(response)
		if decision.Status != IXFRUpToDate && decision.Status != IXFRDiscard {
			t.Fatalf("status = %s, want up-to-date or discard", decision.Status)
		}
		if decision.Status == IXFRDiscard && err == nil {
			t.Fatalf("ApplyIXFR() returned nil error for discarded response")
		}
		if !reflect.DeepEqual(zone, before) {
			t.Fatalf("zone mutated for discarded/up-to-date response:\nbefore=%+v\nafter=%+v", before, zone)
		}
	}
}
