package netsec

import "fmt"

// Example shows the receiving host's typical flow: out-of-order records,
// watermark-issued gap confirmation, a retransmission repair with a
// correction entry, and a full-set audit.
func Example() {
	ledger, _ := NewLedgerWithReference(10)

	// Records arrive out of order; seq 2 and 4 are still in flight.
	_, _ = ledger.IngestRecord(Record{Probe: "edge-7", Gen: 1, Seq: 1, Hash: ContentHash{0x10}})
	_, _ = ledger.IngestRecord(Record{Probe: "edge-7", Gen: 1, Seq: 3, Hash: ContentHash{0x30}})
	_, _ = ledger.IngestRecord(Record{Probe: "edge-7", Gen: 1, Seq: 5, Hash: ContentHash{0x50}})

	// The probe issues its highest generated sequence number.
	commit, _ := ledger.CommitWatermark(Watermark{Probe: "edge-7", Gen: 1, High: 5})
	fmt.Println("confirmed at high=5:", commit.NewlyConfirmed)

	// A retransmission of an existing record is a duplicate, never a gap.
	dup, _ := ledger.IngestRecord(Record{Probe: "edge-7", Gen: 1, Seq: 1, Hash: ContentHash{0x10}})
	fmt.Println("retransmission:", dup.Outcome)

	// A delayed retransmission repairs a confirmed-missing sequence.
	repaired, _ := ledger.IngestRecord(Record{Probe: "edge-7", Gen: 1, Seq: 2, Hash: ContentHash{0x20}})
	fmt.Println("repaired:", repaired.Outcome, "filled:", repaired.FilledMissing,
		"under:", repaired.ConfirmedUnder)

	corrections, _ := ledger.Corrections("edge-7", 1)
	fmt.Println("corrections:", len(corrections))

	report, _ := ledger.QueryRange("edge-7", 1, 1, 5)
	fmt.Println("missing now:", report.Missing)

	audit, _ := ledger.Audit()
	fmt.Println("audit issues:", len(audit.Issues))

	// Output:
	// confirmed at high=5: [{2 2} {4 4}]
	// retransmission: duplicate
	// repaired: accepted filled: true under: 5
	// corrections: 1
	// missing now: [{4 4}]
	// audit issues: 1
}
