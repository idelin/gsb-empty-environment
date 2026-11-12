package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const testLogID = "audit-log/main"

func testPayloads() [][]byte {
	return [][]byte{
		[]byte("record-0"),
		[]byte("secret-record-1"),
		[]byte("record-2"),
		[]byte("secret-record-3"),
		[]byte("record-4"),
		[]byte("record-5"),
		[]byte("record-6"),
		[]byte("record-7"),
		[]byte("record-8"),
	}
}

func makeTestLog(t *testing.T) *Log {
	t.Helper()
	log, err := NewLogFromPayloads(testLogID, testPayloads())
	if err != nil {
		t.Fatalf("NewLogFromPayloads: %v", err)
	}
	return log
}

func cloneExport(t *testing.T, export *Export) *Export {
	t.Helper()
	data, err := MarshalExport(export)
	if err != nil {
		t.Fatalf("MarshalExport: %v", err)
	}
	cloned, err := UnmarshalExport(data)
	if err != nil {
		t.Fatalf("UnmarshalExport: %v", err)
	}
	return cloned
}

func mustVerify(t *testing.T, anchor Anchor, export *Export) *VerifiedLog {
	t.Helper()
	verified, err := Verify(anchor, export)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return verified
}

func mustReject(t *testing.T, anchor Anchor, export *Export) error {
	t.Helper()
	if _, err := Verify(anchor, export); err == nil {
		t.Fatal("verification unexpectedly succeeded")
	} else {
		return err
	}
	return nil
}

func TestEveryPublicSubset(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()
	payloads := testPayloads()
	commitments := log.Commitments()
	count := len(payloads)

	for mask := 0; mask < 1<<count; mask++ {
		reveal := make([]int, 0, count)
		for i := 0; i < count; i++ {
			if mask&(1<<i) != 0 {
				reveal = append(reveal, i)
			}
		}

		export, err := log.Export(reveal)
		if err != nil {
			t.Fatalf("mask %#x: Export: %v", mask, err)
		}
		if export.Count != uint64(count) {
			t.Fatalf("mask %#x: Count = %d, want %d", mask, export.Count, count)
		}
		if !bytes.Equal(export.Root, log.Root) {
			t.Fatalf("mask %#x: export root differs from log root", mask)
		}

		verified := mustVerify(t, anchor, export)
		if verified.Count != uint64(count) {
			t.Fatalf("mask %#x: verified Count = %d, want %d", mask, verified.Count, count)
		}
		for i := 0; i < count; i++ {
			entry := export.Entries[i]
			if entry.Index != i {
				t.Fatalf("mask %#x: entry %d has index %d", mask, i, entry.Index)
			}
			if !bytes.Equal(entry.Commitment, commitments[i]) {
				t.Fatalf("mask %#x: entry %d commitment differs", mask, i)
			}

			public := mask&(1<<i) != 0
			result := verified.Records[i]
			if public {
				if entry.Redacted {
					t.Fatalf("mask %#x: entry %d should be public", mask, i)
				}
				if entry.Seq != uint64(i+1) {
					t.Fatalf("mask %#x: entry %d Seq = %d", mask, i, entry.Seq)
				}
				if !bytes.Equal(entry.Payload, payloads[i]) {
					t.Fatalf("mask %#x: entry %d payload differs", mask, i)
				}
				if !bytes.Equal(result.Payload, payloads[i]) || result.Redacted {
					t.Fatalf("mask %#x: verified entry %d payload differs", mask, i)
				}
			} else {
				if !entry.Redacted {
					t.Fatalf("mask %#x: entry %d should be redacted", mask, i)
				}
				if entry.Seq != 0 || entry.Payload != nil || entry.Digest != nil || entry.Path != nil {
					t.Fatalf("mask %#x: redacted entry %d leaks public data: %#v", mask, i, entry)
				}
				if !result.Redacted || result.Payload != nil {
					t.Fatalf("mask %#x: verified entry %d should be redacted", mask, i)
				}
			}
		}
	}
}

func TestRedactedJSONContainsOnlyCommitment(t *testing.T) {
	log := makeTestLog(t)
	export, err := log.ExportNone()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := MarshalExport(export)
	if err != nil {
		t.Fatalf("MarshalExport: %v", err)
	}

	var decoded struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for i, entry := range decoded.Entries {
		if len(entry) != 3 {
			t.Fatalf("redacted entry %d has fields %v, want only index/redacted/commitment", i, mapKeys(entry))
		}
		for _, field := range []string{"index", "redacted", "commitment"} {
			if _, exists := entry[field]; !exists {
				t.Fatalf("redacted entry %d is missing %q", i, field)
			}
		}
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func TestOmissionRejected(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()

	public, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	for i := 0; i < len(public.Entries); i++ {
		tampered := cloneExport(t, public)
		tampered.Entries = append(tampered.Entries[:i], tampered.Entries[i+1:]...)
		mustReject(t, anchor, tampered)
	}

	mixed, err := log.Export([]int{0, 2, 4, 6, 8})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for i := range mixed.Entries {
		tampered := cloneExport(t, mixed)
		tampered.Entries = append(tampered.Entries[:i], tampered.Entries[i+1:]...)
		mustReject(t, anchor, tampered)
	}

	// Removing a trailing redacted entry while also lowering the stated count
	// must still fail because the root binds the count and all commitments.
	tampered := cloneExport(t, mixed)
	tampered.Entries = tampered.Entries[:len(tampered.Entries)-1]
	tampered.Count--
	mustReject(t, anchor, tampered)

	// Renumbering positions to hide a deletion must not help.
	tampered = cloneExport(t, public)
	tampered.Entries = append(tampered.Entries[:2], tampered.Entries[3:]...)
	for i := range tampered.Entries {
		tampered.Entries[i].Index = i
	}
	mustReject(t, anchor, tampered)
}

func TestReorderRejected(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()

	mixed, err := log.Export([]int{0, 2, 4, 6, 8})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	reversed := cloneExport(t, mixed)
	for left, right := 0, len(reversed.Entries)-1; left < right; left, right = left+1, right-1 {
		reversed.Entries[left], reversed.Entries[right] = reversed.Entries[right], reversed.Entries[left]
	}
	mustReject(t, anchor, reversed)

	// Rewrite the declared indices after the swap.  The payload/commitment
	// pair is then in the wrong position.
	for i := range reversed.Entries {
		reversed.Entries[i].Index = i
	}
	mustReject(t, anchor, reversed)

	// Swap two redacted commitments and correct the structural indices.
	allHidden, err := log.Export(nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	swapped := cloneExport(t, allHidden)
	swapped.Entries[1], swapped.Entries[4] = swapped.Entries[4], swapped.Entries[1]
	for i := range swapped.Entries {
		swapped.Entries[i].Index = i
	}
	mustReject(t, anchor, swapped)

	// Swap two public entries and correct their indices as well.
	allPublic, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	swapped = cloneExport(t, allPublic)
	swapped.Entries[1], swapped.Entries[4] = swapped.Entries[4], swapped.Entries[1]
	for i := range swapped.Entries {
		swapped.Entries[i].Index = i
	}
	mustReject(t, anchor, swapped)
}

func TestDuplicateSequenceRejected(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()
	public, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	duplicate := cloneExport(t, public)
	duplicate.Entries[3].Seq = duplicate.Entries[1].Seq
	mustReject(t, anchor, duplicate)

	duplicateEntry := cloneExport(t, public)
	duplicateEntry.Entries[2] = duplicateEntry.Entries[1]
	duplicateEntry.Entries[2].Index = 2
	mustReject(t, anchor, duplicateEntry)

	publicRecords := []Record{
		{Seq: 1, Payload: []byte("a")},
		{Seq: 1, Payload: []byte("b")},
	}
	if _, err := NewLog("duplicate-log", publicRecords); err == nil {
		t.Fatal("NewLog accepted duplicate sequence numbers")
	}
}

func TestCrossLogSpliceRejected(t *testing.T) {
	logA := makeTestLog(t)

	otherPayloads := testPayloads()
	for i := range otherPayloads {
		otherPayloads[i] = append([]byte("other:"), otherPayloads[i]...)
	}
	logB, err := NewLogFromPayloads("audit-log/other", otherPayloads)
	if err != nil {
		t.Fatalf("NewLogFromPayloads: %v", err)
	}
	if bytes.Equal(logA.Root, logB.Root) {
		t.Fatal("different logs have the same root")
	}

	publicA, err := logA.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll A: %v", err)
	}
	publicB, err := logB.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll B: %v", err)
	}

	// Splice a public entry from B into A.
	spliced := cloneExport(t, publicA)
	spliced.Entries[3] = publicB.Entries[3]
	mustReject(t, logA.Anchor(), spliced)

	// Splice only a redacted commitment from B into A.
	hiddenA, err := logA.Export(nil)
	if err != nil {
		t.Fatalf("Export A: %v", err)
	}
	hiddenB, err := logB.Export(nil)
	if err != nil {
		t.Fatalf("Export B: %v", err)
	}
	splicedHidden := cloneExport(t, hiddenA)
	splicedHidden.Entries[2].Commitment = hiddenB.Entries[2].Commitment
	mustReject(t, logA.Anchor(), splicedHidden)

	// Present B's whole package under A's anchor.
	mustReject(t, logA.Anchor(), publicB)

	// Relabel B's entries as A and keep A's root: the root no longer matches.
	relabelled := cloneExport(t, publicB)
	relabelled.LogID = logA.ID
	relabelled.Root = cloneBytes(logA.Root)
	mustReject(t, logA.Anchor(), relabelled)
}

func TestIdenticalPayloadsDifferentLogsHaveDifferentRoots(t *testing.T) {
	logA, err := NewLogFromPayloads("log-a", testPayloads())
	if err != nil {
		t.Fatalf("NewLogFromPayloads: %v", err)
	}
	logB, err := NewLogFromPayloads("log-b", testPayloads())
	if err != nil {
		t.Fatalf("NewLogFromPayloads: %v", err)
	}
	if bytes.Equal(logA.Root, logB.Root) {
		t.Fatal("log ID is not bound into the root")
	}
	if bytes.Equal(logA.Commitments()[0], logB.Commitments()[0]) {
		t.Fatal("log ID is not bound into the leaf commitment")
	}
}

func TestExplicitNonContiguousSequences(t *testing.T) {
	records := []Record{
		{Seq: 10, Payload: []byte("ten")},
		{Seq: 20, Payload: []byte("twenty")},
		{Seq: 30, Payload: []byte("thirty")},
		{Seq: 40, Payload: []byte("forty")},
	}
	log, err := NewLog("gapped-log", records)
	if err != nil {
		t.Fatalf("NewLog: %v", err)
	}
	export, err := log.Export([]int{1, 3})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	verified, err := Verify(log.Anchor(), export)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Records[1].Seq != 20 || verified.Records[3].Seq != 40 {
		t.Fatalf("revealed sequence numbers were not preserved: %#v", verified.Records)
	}
}

func TestCountIsBoundByRoot(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()
	public, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	changedCount := cloneExport(t, public)
	changedCount.Count++
	mustReject(t, anchor, changedCount)

	// Dropping the final record and reducing the stated count must not
	// reproduce the old root.
	shortened := cloneExport(t, public)
	shortened.Entries = shortened.Entries[:len(shortened.Entries)-1]
	shortened.Count--
	mustReject(t, anchor, shortened)
}

func TestPayloadAndProofTamperingRejected(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()
	public, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	tampered := cloneExport(t, public)
	tampered.Entries[0].Payload = []byte("tampered")
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, public)
	tampered.Entries[0].Digest[0] ^= 0xff
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, public)
	tampered.Entries[0].Commitment[0] ^= 0xff
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, public)
	tampered.Entries[0].Seq = 99
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, public)
	if len(tampered.Entries[0].Path) == 0 {
		t.Fatal("expected a non-empty path for a multi-record log")
	}
	tampered.Entries[0].Path[0].Sibling[0] ^= 0xff
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, public)
	tampered.Entries[0].Path = tampered.Entries[0].Path[:len(tampered.Entries[0].Path)-1]
	mustReject(t, anchor, tampered)

	// A redacted entry may not smuggle public data alongside its commitment.
	hidden, err := log.Export(nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	tampered = cloneExport(t, hidden)
	tampered.Entries[1].Payload = []byte("leak")
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, hidden)
	tampered.Entries[1].Seq = 2
	mustReject(t, anchor, tampered)

	tampered = cloneExport(t, hidden)
	tampered.Entries[1].Path = []PathStep{{Sibling: make([]byte, 32)}}
	mustReject(t, anchor, tampered)
}

func TestJSONRoundTripAndStrictDecoding(t *testing.T) {
	log := makeTestLog(t)
	anchor := log.Anchor()
	export, err := log.Export([]int{0, 3, 7})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	data, err := MarshalExport(export)
	if err != nil {
		t.Fatalf("MarshalExport: %v", err)
	}
	verified, err := VerifyJSON(anchor, data)
	if err != nil {
		t.Fatalf("VerifyJSON: %v", err)
	}
	if verified.Count != log.Count {
		t.Fatalf("Count = %d, want %d", verified.Count, log.Count)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	raw["unknown"] = true
	withUnknown, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if _, err := UnmarshalExport(withUnknown); err == nil {
		t.Fatal("UnmarshalExport accepted an unknown field")
	}

	if _, err := UnmarshalExport(append(data, []byte("{}")...)); err == nil {
		t.Fatal("UnmarshalExport accepted trailing JSON")
	}
}

func TestNewLogValidation(t *testing.T) {
	if _, err := NewLog("", nil); err == nil {
		t.Fatal("NewLog accepted an empty log ID")
	}
	if _, err := NewLog("x", []Record{{Seq: 0, Payload: []byte("a")}}); err == nil {
		t.Fatal("NewLog accepted sequence number 0")
	}
	if _, err := NewLog("x", []Record{{Seq: 2}, {Seq: 1}}); err == nil {
		t.Fatal("NewLog accepted descending sequences")
	}

	log, err := NewLogFromPayloads("empty", nil)
	if err != nil {
		t.Fatalf("NewLogFromPayloads empty: %v", err)
	}
	export, err := log.Export(nil)
	if err != nil {
		t.Fatalf("Export empty: %v", err)
	}
	if _, err := Verify(log.Anchor(), export); err != nil {
		t.Fatalf("Verify empty: %v", err)
	}
}

func TestExportValidation(t *testing.T) {
	log := makeTestLog(t)
	for _, reveal := range [][]int{{-1}, {len(log.entries)}, {1, 1}} {
		if _, err := log.Export(reveal); err == nil {
			t.Fatalf("Export accepted invalid reveal list %v", reveal)
		}
	}
}

func TestVerifyRejectsMalformedAnchorAndHeader(t *testing.T) {
	log := makeTestLog(t)
	export, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	cases := []struct {
		name   string
		anchor Anchor
		export *Export
	}{
		{"empty anchor log ID", Anchor{Root: log.Root}, export},
		{"short anchor root", Anchor{LogID: log.ID, Root: log.Root[:16]}, export},
		{"wrong anchor log ID", Anchor{LogID: "other", Root: log.Root}, export},
		{"wrong anchor root", Anchor{LogID: log.ID, Root: make([]byte, 32)}, export},
		{"nil export", log.Anchor(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Verify(tc.anchor, tc.export); err == nil {
				t.Fatal("Verify accepted malformed input")
			}
		})
	}

	shortRoot := cloneExport(t, export)
	shortRoot.Root = shortRoot.Root[:16]
	mustReject(t, log.Anchor(), shortRoot)

	countMismatch := cloneExport(t, export)
	countMismatch.Entries = countMismatch.Entries[:1]
	mustReject(t, log.Anchor(), countMismatch)

	slotMismatch := cloneExport(t, export)
	slotMismatch.Entries[0].Index = 1
	mustReject(t, log.Anchor(), slotMismatch)
}

func Example() {
	log, _ := NewLogFromPayloads("audit-2026-11", [][]byte{
		[]byte("public event"),
		[]byte("private event"),
		[]byte("public event 2"),
	})
	export, _ := log.Export([]int{0, 2})
	verified, err := Verify(log.Anchor(), export)
	if err != nil {
		panic(err)
	}

	fmt.Println(verified.Count)
	fmt.Println(string(verified.Records[0].Payload))
	fmt.Println(verified.Records[1].Redacted)
	fmt.Println(string(verified.Records[2].Payload))
	// Output:
	// 3
	// public event
	// true
	// public event 2
}

func TestPathAndRootAreDomainSeparated(t *testing.T) {
	log := makeTestLog(t)
	export, err := log.ExportAll()
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	leaf := export.Entries[0].Commitment
	if bytes.Equal(leaf, nodeCommitment(leaf, leaf)) {
		t.Fatal("leaf and internal node domains collide")
	}
	if strings.Contains(string(leaf), "record-0") {
		t.Fatal("commitment leaks payload")
	}
}
