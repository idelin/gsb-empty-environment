package csvq

import (
	"fmt"
	"strings"
	"testing"
)

// buildSource builds a source with quoted commas, quoted newlines,
// short rows, and over-wide (quarantined) rows.
func buildSource() []byte {
	var b strings.Builder
	b.WriteString("id,code,note\n")
	for i := 0; i < 40; i++ {
		switch {
		case i == 7 || i == 23:
			fmt.Fprintf(&b, "%d,0%d,note,extra\n", i, i) // too many fields
		case i%5 == 4:
			fmt.Fprintf(&b, "%d,0%d\n", i, i) // short row
		case i%3 == 2:
			fmt.Fprintf(&b, "%d,0%d,\"line1\nline2\"\n", i, i) // quoted newline
		default:
			fmt.Fprintf(&b, "%d,0%d,\"a,b\"\n", i, i) // quoted comma
		}
	}
	return []byte(b.String())
}

func TestVerifyChunksAndSamples(t *testing.T) {
	src := buildSource()
	tab, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Quarantine) != 2 {
		t.Fatalf("quarantined = %d, want 2", len(tab.Quarantine))
	}
	rep, err := Verify(src, tab, 8)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Chunks != 5 { // 40 items / 8 per chunk
		t.Fatalf("chunks = %d, want 5", rep.Chunks)
	}
	if rep.Records != 38 || rep.Quarantined != 2 {
		t.Fatalf("report = %+v", rep)
	}
	// Chunks tile the data section contiguously.
	chunks := EnumerateChunks(tab, 8)
	if chunks[0].StartOffset != tab.DataOffset {
		t.Fatalf("first chunk starts at %d, data at %d", chunks[0].StartOffset, tab.DataOffset)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].StartOffset != chunks[i-1].EndOffset {
			t.Fatalf("chunk %d not contiguous with chunk %d", i, i-1)
		}
	}
	if chunks[len(chunks)-1].EndOffset != int64(len(src)) {
		t.Fatalf("last chunk ends at %d, source %d bytes", chunks[len(chunks)-1].EndOffset, len(src))
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	src := buildSource()
	tab, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a digit inside a valid record: independent re-parse of the
	// sampled range must disagree with the recovered table.
	bad := append([]byte(nil), src...)
	for i, r := range tab.Records {
		if r.Seq%5 != 4 { // a full-width record
			pos := int(r.Offset)
			bad[pos] = '9'
			if _, err := Verify(bad, tab, 8); err == nil {
				t.Fatalf("tamper at record %d (offset %d) not detected", i, pos)
			}
			return
		}
	}
	t.Fatal("no suitable record found to tamper")
}

func TestVerifyDetectsChunkShift(t *testing.T) {
	src := buildSource()
	tab, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	chunks := EnumerateChunks(tab, 8)
	chunks[2].StartOffset-- // break contiguity
	if err := VerifyChunks(src, tab, chunks); err == nil {
		t.Fatal("shifted chunk boundary not detected")
	}
}

func TestVerifyQuarantinedSampleRecovery(t *testing.T) {
	src := buildSource()
	tab, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	// Every quarantined row is sampled and re-parsed from its raw range.
	samples := SampleItems(tab, 4)
	qs := 0
	for _, it := range samples {
		if it.Quarantine != nil {
			qs++
		}
	}
	if qs != len(tab.Quarantine) {
		t.Fatalf("sampled %d quarantined rows, want %d", qs, len(tab.Quarantine))
	}
	if err := VerifyItems(src, tab.Header.Len(), samples); err != nil {
		t.Fatal(err)
	}
}
