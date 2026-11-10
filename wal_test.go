package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// walBuilder mimics the LevelDB log writer: 32KB blocks, 7-byte headers,
// zero-padded trailers, masked CRC32C, FULL/FIRST/MIDDLE/LAST fragments.
type walBuilder struct {
	buf      bytes.Buffer
	blockOff int
}

func (w *walBuilder) emitFragment(recType byte, payload []byte, corruptCRC bool) {
	if blockSize-w.blockOff < headerSize {
		w.buf.Write(make([]byte, blockSize-w.blockOff))
		w.blockOff = 0
	}
	crc := maskCRC(crc32.Checksum(append([]byte{recType}, payload...), castagnoli))
	if corruptCRC {
		crc ^= 0xff
	}
	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], crc)
	binary.LittleEndian.PutUint16(hdr[4:6], uint16(len(payload)))
	hdr[6] = recType
	w.buf.Write(hdr[:])
	w.buf.Write(payload)
	w.blockOff += headerSize + len(payload)
}

// addRecord fragments one logical record exactly like log_writer.cc.
func (w *walBuilder) addRecord(payload []byte) {
	left := len(payload)
	begin := true
	for {
		leftover := blockSize - w.blockOff
		if leftover < headerSize {
			w.buf.Write(make([]byte, leftover))
			w.blockOff = 0
			leftover = blockSize
		}
		avail := leftover - headerSize
		end := left <= avail
		var recType byte
		switch {
		case begin && end:
			recType = recFull
		case begin:
			recType = recFirst
		case end:
			recType = recLast
		default:
			recType = recMiddle
		}
		fragLen := avail
		if end {
			fragLen = left
		}
		w.emitFragment(recType, payload[len(payload)-left:len(payload)-left+fragLen], false)
		left -= fragLen
		begin = false
		if end {
			return
		}
	}
}

func makeBatch(seq uint64, ops ...BatchOp) []byte {
	var b bytes.Buffer
	var hdr [batchHeaderSize]byte
	binary.LittleEndian.PutUint64(hdr[0:8], seq)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(ops)))
	b.Write(hdr[:])
	for _, op := range ops {
		if op.Del {
			b.WriteByte(tagDeletion)
		} else {
			b.WriteByte(tagValue)
		}
		writeVarint32(&b, uint32(len(op.Key)))
		b.Write(op.Key)
		if !op.Del {
			writeVarint32(&b, uint32(len(op.Value)))
			b.Write(op.Value)
		}
	}
	return b.Bytes()
}

func writeVarint32(b *bytes.Buffer, v uint32) {
	var tmp [binary.MaxVarintLen32]byte
	n := binary.PutUvarint(tmp[:], uint64(v))
	b.Write(tmp[:n])
}

func put(key, val string) BatchOp { return BatchOp{Key: []byte(key), Value: []byte(val)} }
func del(key string) BatchOp      { return BatchOp{Del: true, Key: []byte(key)} }

// padToBlockBoundary zero-fills to the next 32KB block boundary.
func (w *walBuilder) padToBlockBoundary() {
	if w.blockOff == 0 {
		return
	}
	w.buf.Write(make([]byte, blockSize-w.blockOff))
	w.blockOff = 0
}

func readAll(t *testing.T, data []byte) (*History, *LogReader) {
	t.Helper()
	lr := NewLogReader(bytes.NewReader(data))
	hist := NewHistory()
	for {
		rec := lr.Next()
		if rec == nil {
			break
		}
		b, err := parseBatch(rec.Data)
		if err != nil {
			t.Fatalf("parseBatch: %v", err)
		}
		hist.Apply(b)
	}
	return hist, lr
}

func TestRoundTripAndQuery(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("secret.token", "v1"), put("other", "x")))
	w.addRecord(makeBatch(3, put("secret.token", "v2")))
	w.addRecord(makeBatch(4, del("secret.token")))

	hist, lr := readAll(t, w.buf.Bytes())
	if len(lr.Corruptions) != 0 {
		t.Fatalf("unexpected corruptions: %v", lr.Corruptions)
	}
	reps := hist.Query(func(k string) bool { return strings.HasPrefix(k, "secret.") })
	if len(reps) != 1 {
		t.Fatalf("want 1 report, got %d", len(reps))
	}
	r := reps[0]
	if !r.HasFirst || r.FirstWrite.Seq != 1 || string(r.FirstWrite.Value) != "v1" {
		t.Fatalf("first write wrong: %+v", r.FirstWrite)
	}
	if r.Visible || r.Last.Seq != 4 {
		t.Fatalf("last version should be deletion at seq 4, got %+v", r.Last)
	}
}

func TestSameBatchPutThenDelete(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("k", "v1"), del("k")))
	hist, _ := readAll(t, w.buf.Bytes())
	reps := hist.Query(func(k string) bool { return k == "k" })
	if len(reps) != 1 {
		t.Fatalf("want 1 report, got %d", len(reps))
	}
	if reps[0].Visible {
		t.Fatalf("same-batch put-then-delete must end deleted")
	}
	if reps[0].Last.Seq != 2 {
		t.Fatalf("delete should carry seq 2, got %d", reps[0].Last.Seq)
	}
	if !reps[0].HasFirst || string(reps[0].FirstWrite.Value) != "v1" {
		t.Fatalf("first write should still be v1")
	}
}

func TestFragmentationAcrossBlocks(t *testing.T) {
	var w walBuilder
	big := strings.Repeat("A", blockSize*2+100) // forces FIRST/MIDDLE/LAST
	w.addRecord(makeBatch(1, put("big", big)))
	w.addRecord(makeBatch(2, put("after", "ok")))
	hist, lr := readAll(t, w.buf.Bytes())
	if len(lr.Corruptions) != 0 {
		t.Fatalf("unexpected corruptions: %v", lr.Corruptions)
	}
	reps := hist.Query(func(string) bool { return true })
	if len(reps) != 2 {
		t.Fatalf("want 2 keys, got %d", len(reps))
	}
	if string(reps[0].Last.Value) != big && string(reps[1].Last.Value) != big {
		t.Fatalf("fragmented value not reassembled")
	}
}

func TestCorruptCRCDropsRecordOnly(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("a", "1")))
	w.emitFragment(recFull, makeBatch(2, put("b", "2")), true) // corrupt
	// A CRC failure skips to the end of the block, so the next
	// surviving record must start in a fresh block.
	w.padToBlockBoundary()
	w.addRecord(makeBatch(3, put("c", "3")))

	lr := NewLogReader(bytes.NewReader(w.buf.Bytes()))
	hist := NewHistory()
	n := 0
	for {
		rec := lr.Next()
		if rec == nil {
			break
		}
		n++
		b, err := parseBatch(rec.Data)
		if err != nil {
			t.Fatalf("parseBatch: %v", err)
		}
		hist.Apply(b)
	}
	if n != 2 {
		t.Fatalf("want 2 surviving records, got %d", n)
	}
	if len(lr.Corruptions) != 1 {
		t.Fatalf("want 1 corruption, got %v", lr.Corruptions)
	}
	reps := hist.Query(func(string) bool { return true })
	if len(reps) != 2 {
		t.Fatalf("corrupted batch leaked into history: %v", reps)
	}
}

func TestTruncatedTailNoFakeCommit(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("a", "1")))
	// Start a fragmented record, then truncate before LAST.
	w.emitFragment(recFirst, makeBatch(2, put("b", "2"))[:8], false)
	data := w.buf.Bytes()

	lr := NewLogReader(bytes.NewReader(data))
	hist := NewHistory()
	for {
		rec := lr.Next()
		if rec == nil {
			break
		}
		b, err := parseBatch(rec.Data)
		if err != nil {
			t.Fatalf("parseBatch: %v", err)
		}
		hist.Apply(b)
	}
	reps := hist.Query(func(string) bool { return true })
	if len(reps) != 1 || reps[0].Key != "a" {
		t.Fatalf("partial record formed a fake commit: %v", reps)
	}
	if len(lr.Corruptions) == 0 {
		t.Fatalf("expected dropped-partial corruption report")
	}
}

func TestCountMismatchRejected(t *testing.T) {
	raw := makeBatch(1, put("a", "1"))
	binary.LittleEndian.PutUint32(raw[8:12], 5) // lie about the count
	if _, err := parseBatch(raw); err == nil {
		t.Fatalf("count mismatch must be rejected")
	}
}

func TestNonZeroPaddingReported(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("a", "1")))
	// Force a trailer then poison it.
	for blockSize-w.blockOff >= headerSize {
		w.emitFragment(recFull, nil, false)
	}
	pad := blockSize - w.blockOff
	w.buf.Write(bytes.Repeat([]byte{0xAA}, pad))
	w.blockOff = 0
	w.addRecord(makeBatch(2, put("b", "2")))

	lr := NewLogReader(bytes.NewReader(w.buf.Bytes()))
	n := 0
	for lr.Next() != nil {
		n++
	}
	if n == 0 {
		t.Fatalf("no records read")
	}
	found := false
	for _, c := range lr.Corruptions {
		if strings.Contains(c.Reason, "padding") {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-zero padding not reported: %v", lr.Corruptions)
	}
}

func TestSequenceGapReported(t *testing.T) {
	var w walBuilder
	w.addRecord(makeBatch(1, put("a", "1")))
	w.addRecord(makeBatch(7, put("b", "2"))) // gap: 2..6 missing
	hist, _ := readAll(t, w.buf.Bytes())
	if len(hist.Anomalies) != 1 || !strings.Contains(hist.Anomalies[0], "gap") {
		t.Fatalf("expected sequence gap anomaly, got %v", hist.Anomalies)
	}
}

func TestMiddleWithoutFirstDropped(t *testing.T) {
	var w walBuilder
	w.emitFragment(recMiddle, []byte("orphan"), false)
	w.addRecord(makeBatch(1, put("a", "1")))
	lr := NewLogReader(bytes.NewReader(w.buf.Bytes()))
	rec := lr.Next()
	if rec == nil {
		t.Fatalf("expected the valid record")
	}
	b, err := parseBatch(rec.Data)
	if err != nil || b.Ops[0].Value[0] != '1' {
		t.Fatalf("orphan fragment polluted record: %v %v", b, err)
	}
	if len(lr.Corruptions) != 1 {
		t.Fatalf("want orphan reported, got %v", lr.Corruptions)
	}
}
