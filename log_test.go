package manifestmgr

import (
	"encoding/binary"
	"strings"
	"testing"
)

// helper: build a store with two committed versions, return the FS and the
// raw log bytes.
func twoVersionStore(t *testing.T) (*FaultFS, []byte) {
	t.Helper()
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(map[string][]byte{"a": []byte("alpha-1")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(map[string][]byte{"b": []byte("beta-2")}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := ff.Inner.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return ff, raw
}

func TestLogRoundtrip(t *testing.T) {
	_, raw := twoVersionStore(t)
	recs, goodLen, torn, err := replayLog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if torn || goodLen != len(raw) {
		t.Fatalf("clean log flagged torn: goodLen=%d len=%d", goodLen, len(raw))
	}
	if len(recs) != 2 || recs[0].Version != 1 || recs[1].Version != 2 {
		t.Fatalf("unexpected records: %+v", recs)
	}
}

// An incomplete trailing record (cut at any byte) must be tolerated and
// truncated on open.
func TestTornTailTolerated(t *testing.T) {
	for _, cut := range []int{1, 2, 3, 5, 17, 40} {
		ff, raw := twoVersionStore(t)
		// Find start of second frame, then truncate `cut` bytes into it.
		n := int(binary.BigEndian.Uint32(raw[:4]))
		second := 4 + n + 4
		truncated := raw[:second+cut]
		if err := ff.Inner.WriteFile(logPath, truncated); err != nil {
			t.Fatal(err)
		}
		m, err := Open(ff)
		if err != nil {
			t.Fatalf("cut=%d: open should tolerate torn tail: %v", cut, err)
		}
		if m.Version() != 1 {
			t.Fatalf("cut=%d: version = %d, want 1", cut, m.Version())
		}
		// Log must have been truncated back to the good prefix.
		after, _ := ff.Inner.ReadFile(logPath)
		if len(after) != second {
			t.Fatalf("cut=%d: log not truncated to %d bytes (got %d)", cut, second, len(after))
		}
		// Store must remain usable: a new version 2 can be committed.
		if _, err := m.Publish(map[string][]byte{"c": []byte("gamma")}, nil); err != nil {
			t.Fatalf("cut=%d: publish after recovery: %v", cut, err)
		}
	}
}

// A complete frame with a bad CRC is a hard error, not a torn tail.
func TestCorruptCompleteRecordRejected(t *testing.T) {
	ff, raw := twoVersionStore(t)
	n := int(binary.BigEndian.Uint32(raw[:4]))
	second := 4 + n + 4
	// Flip a payload byte inside the second (complete) frame.
	raw[second+6] ^= 0xFF
	if err := ff.Inner.WriteFile(logPath, raw); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ff)
	if err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Fatalf("want CRC corruption error, got %v", err)
	}
}

// Corruption in a non-last record must also be rejected.
func TestCorruptFirstRecordRejected(t *testing.T) {
	ff, raw := twoVersionStore(t)
	raw[7] ^= 0xFF // payload of frame 1
	if err := ff.Inner.WriteFile(logPath, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ff); err == nil {
		t.Fatal("corrupt first record not detected")
	}
}

// An absurd length field is corruption, not a torn tail.
func TestAbsurdLengthRejected(t *testing.T) {
	ff, raw := twoVersionStore(t)
	n := int(binary.BigEndian.Uint32(raw[:4]))
	second := 4 + n + 4
	bad := append([]byte(nil), raw[:second]...)
	bad = append(bad, 0xFF, 0xFF, 0xFF, 0xFF)
	if err := ff.Inner.WriteFile(logPath, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ff); err == nil {
		t.Fatal("absurd length not rejected")
	}
}

// A gap in the version chain must be rejected.
func TestVersionChainGapRejected(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	if _, err := Create(ff); err != nil {
		t.Fatal(err)
	}
	rec := &Record{Version: 5, Segments: nil}
	frame, err := encodeFrame(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Inner.AppendFile(logPath, frame); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ff)
	if err == nil || !strings.Contains(err.Error(), "version chain") {
		t.Fatalf("want version chain error, got %v", err)
	}
}

// A segment table that does not match the added/removed deltas is rejected.
func TestInconsistentSegmentTableRejected(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(map[string][]byte{"a": []byte("alpha")}, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := ff.Inner.ReadFile(logPath)
	// Forge a v2 whose deltas don't produce its segment table.
	ghost := Segment{ID: "seg-ghost", Digest: DigestOf([]byte("x")), Size: 1}
	prev := m.Versions()[0]
	rec2, err := buildRecord(prev, nil, nil, m.lastDigest)
	if err != nil {
		t.Fatal(err)
	}
	rec2.Segments = append(rec2.Segments, ghost) // table inconsistent with deltas
	frame, _ := encodeFrame(rec2)
	if err := ff.Inner.AppendFile(logPath, frame); err != nil {
		t.Fatal(err)
	}
	_ = raw
	if _, err := Open(ff); err == nil {
		t.Fatal("inconsistent segment table not detected")
	}
}
