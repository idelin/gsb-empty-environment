package csvq

import "fmt"

// Chunk is an enumerated, contiguous block of items over record
// boundaries. Chunk byte ranges tile the data section of the source.
type Chunk struct {
	Index       int
	StartSeq    int // first item sequence in the chunk
	EndSeq      int // one past the last item sequence
	StartOffset int64
	EndOffset   int64
	Records     int
	Quarantined int
}

// Report summarizes a successful Verify run.
type Report struct {
	Chunks      int
	Records     int
	Quarantined int
	Samples     int
}

// EnumerateChunks groups the table items into chunks of size items.
func EnumerateChunks(t *Table, size int) []Chunk {
	if size <= 0 {
		size = 16
	}
	var chunks []Chunk
	for i := 0; i < len(t.Items); i += size {
		j := i + size
		if j > len(t.Items) {
			j = len(t.Items)
		}
		ch := Chunk{
			Index:       len(chunks),
			StartSeq:    t.Items[i].Seq,
			EndSeq:      t.Items[j-1].Seq + 1,
			StartOffset: t.Items[i].Offset,
			EndOffset:   t.Items[j-1].EndOffset,
		}
		for _, it := range t.Items[i:j] {
			if it.Record != nil {
				ch.Records++
			} else {
				ch.Quarantined++
			}
		}
		chunks = append(chunks, ch)
	}
	return chunks
}

// VerifyChunks checks that chunks tile the source data section without
// gaps or overlaps, and independently re-parses each chunk's byte range
// to confirm the record and quarantine counts.
func VerifyChunks(src []byte, t *Table, chunks []Chunk) error {
	p := NewParser()
	width := t.Header.Len()
	prevEnd := t.DataOffset
	prevSeq := 0
	for _, ch := range chunks {
		if ch.StartOffset != prevEnd {
			return fmt.Errorf("csvq: chunk %d starts at %d, want %d (gap or overlap)", ch.Index, ch.StartOffset, prevEnd)
		}
		if ch.StartSeq != prevSeq {
			return fmt.Errorf("csvq: chunk %d starts at seq %d, want %d", ch.Index, ch.StartSeq, prevSeq)
		}
		records, quarantine := p.parseItems(src[ch.StartOffset:ch.EndOffset], ch.StartOffset, width, ch.StartSeq)
		if len(records) != ch.Records || len(quarantine) != ch.Quarantined {
			return fmt.Errorf("csvq: chunk %d re-parse got %d records and %d quarantined, want %d and %d",
				ch.Index, len(records), len(quarantine), ch.Records, ch.Quarantined)
		}
		prevEnd = ch.EndOffset
		prevSeq = ch.EndSeq
	}
	if prevEnd != int64(len(src)) {
		return fmt.Errorf("csvq: chunks end at %d, source is %d bytes", prevEnd, len(src))
	}
	return nil
}

// SampleItems picks a deterministic sample: every quarantined row plus
// evenly spaced valid records (at most maxValid of them; <=0 means all).
func SampleItems(t *Table, maxValid int) []Item {
	var valid []Item
	var picked []Item
	for _, it := range t.Items {
		if it.Record != nil {
			valid = append(valid, it)
		} else {
			picked = append(picked, it)
		}
	}
	stride := 1
	if maxValid > 0 && len(valid) > maxValid {
		stride = (len(valid) + maxValid - 1) / maxValid
	}
	sampled := make([]Item, 0, len(picked)+len(valid)/stride+1)
	sampled = append(sampled, picked...)
	for i := 0; i < len(valid); i += stride {
		sampled = append(sampled, valid[i])
	}
	// restore source order
	for i := 1; i < len(sampled); i++ {
		for j := i; j > 0 && sampled[j-1].Seq > sampled[j].Seq; j-- {
			sampled[j-1], sampled[j] = sampled[j], sampled[j-1]
		}
	}
	return sampled
}

// VerifyItems independently re-parses each sampled item from its raw
// byte range and compares it against the table's recovered content,
// confirming both the original ranges and the quarantine recovery.
func VerifyItems(src []byte, width int, items []Item) error {
	p := NewParser()
	for _, it := range items {
		if it.Offset < 0 || it.EndOffset > int64(len(src)) || it.Offset >= it.EndOffset {
			return fmt.Errorf("csvq: item %d has invalid range [%d,%d)", it.Seq, it.Offset, it.EndOffset)
		}
		records, quarantine := p.parseItems(src[it.Offset:it.EndOffset], it.Offset, width, it.Seq)
		if got := len(records) + len(quarantine); got != 1 {
			return fmt.Errorf("csvq: item %d range re-parsed into %d items, want 1", it.Seq, got)
		}
		if it.Record != nil {
			if len(records) != 1 || !equalRecord(*it.Record, records[0]) {
				return fmt.Errorf("csvq: item %d record mismatch on independent re-parse", it.Seq)
			}
		} else {
			if len(quarantine) != 1 || !equalQuarantine(*it.Quarantine, quarantine[0]) {
				return fmt.Errorf("csvq: item %d quarantine mismatch on independent re-parse", it.Seq)
			}
		}
	}
	return nil
}

// Verify enumerates chunks over the table, checks that they tile the
// source, independently re-parses every chunk, and independently
// re-parses sampled records (all quarantined rows included) from their
// original byte ranges.
func Verify(src []byte, t *Table, chunkSize int) (*Report, error) {
	chunks := EnumerateChunks(t, chunkSize)
	if err := VerifyChunks(src, t, chunks); err != nil {
		return nil, err
	}
	samples := SampleItems(t, 32)
	if err := VerifyItems(src, t.Header.Len(), samples); err != nil {
		return nil, err
	}
	return &Report{
		Chunks:      len(chunks),
		Records:     len(t.Records),
		Quarantined: len(t.Quarantine),
		Samples:     len(samples),
	}, nil
}

func equalRecord(a, b Record) bool {
	if a.Seq != b.Seq || a.Offset != b.Offset || a.EndOffset != b.EndOffset {
		return false
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i] != b.Fields[i] {
			return false
		}
	}
	return true
}

func equalQuarantine(a, b QuarantinedRow) bool {
	if a.Seq != b.Seq || a.Offset != b.Offset || a.EndOffset != b.EndOffset || a.Reason != b.Reason {
		return false
	}
	if len(a.Raw) != len(b.Raw) {
		return false
	}
	for i := range a.Raw {
		if a.Raw[i] != b.Raw[i] {
			return false
		}
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i] != b.Fields[i] {
			return false
		}
	}
	return true
}
