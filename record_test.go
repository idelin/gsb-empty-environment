package aries

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		rec  *Record
	}{
		{"update", &Record{Type: TypeUpdate, TxnID: 7, PrevLSN: 100, PageID: 3, Offset: 64, OldData: []byte("old-value"), NewData: []byte("new-value")}},
		{"update-empty-data", &Record{Type: TypeUpdate, TxnID: 1, PageID: 9, Offset: 0}},
		{"update-max-lens", &Record{Type: TypeUpdate, TxnID: ^uint64(0), PrevLSN: ^uint64(0), PageID: ^uint32(0), Offset: ^uint32(0), OldData: bytes.Repeat([]byte{0xAB}, 300), NewData: bytes.Repeat([]byte{0xCD}, 300)}},
		{"clr", &Record{Type: TypeCLR, TxnID: 7, PrevLSN: 100, PageID: 3, Offset: 64, OldData: []byte("n"), NewData: []byte("o"), UndoNextLSN: 42}},
		{"commit", &Record{Type: TypeCommit, TxnID: 11, PrevLSN: 555}},
		{"abort", &Record{Type: TypeAbort, TxnID: 12, PrevLSN: 556}},
		{"end", &Record{Type: TypeEnd, TxnID: 13, PrevLSN: 557}},
		{"begin-checkpoint", &Record{Type: TypeBeginCheckpoint}},
		{"end-checkpoint", &Record{
			Type: TypeEndCheckpoint, BeginLSN: 8,
			Txns: []TxnSnapshot{
				{TxnID: 1, LastLSN: 50, Status: byte(StatusRunning)},
				{TxnID: 2, LastLSN: 60, Status: byte(StatusCommitted)},
			},
			DirtyPages: []DirtyPageSnapshot{
				{PageID: 10, RecLSN: 50},
				{PageID: 20, RecLSN: 60},
			},
		}},
		{"end-checkpoint-empty", &Record{Type: TypeEndCheckpoint, BeginLSN: 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := tt.rec.Encode()
			got, n, err := DecodeRecord(buf, CurrentVersion)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("consumed %d bytes, want %d", n, len(buf))
			}
			if got.Type != tt.rec.Type || got.TxnID != tt.rec.TxnID || got.PrevLSN != tt.rec.PrevLSN {
				t.Fatalf("header mismatch: got %+v want %+v", got, tt.rec)
			}
			if got.PageID != tt.rec.PageID || got.Offset != tt.rec.Offset {
				t.Fatalf("page fields mismatch: got %+v want %+v", got, tt.rec)
			}
			if !bytes.Equal(got.OldData, tt.rec.OldData) || !bytes.Equal(got.NewData, tt.rec.NewData) {
				t.Fatalf("data mismatch: got %q/%q want %q/%q", got.OldData, got.NewData, tt.rec.OldData, tt.rec.NewData)
			}
			if got.UndoNextLSN != tt.rec.UndoNextLSN {
				t.Fatalf("undoNextLSN: got %d want %d", got.UndoNextLSN, tt.rec.UndoNextLSN)
			}
			if got.BeginLSN != tt.rec.BeginLSN {
				t.Fatalf("beginLSN: got %d want %d", got.BeginLSN, tt.rec.BeginLSN)
			}
			if len(got.Txns) != len(tt.rec.Txns) || len(got.DirtyPages) != len(tt.rec.DirtyPages) {
				t.Fatalf("checkpoint snapshot size mismatch")
			}
			for i := range got.Txns {
				if got.Txns[i] != tt.rec.Txns[i] {
					t.Fatalf("txn snapshot %d: got %+v want %+v", i, got.Txns[i], tt.rec.Txns[i])
				}
			}
			for i := range got.DirtyPages {
				if got.DirtyPages[i] != tt.rec.DirtyPages[i] {
					t.Fatalf("dpt snapshot %d: got %+v want %+v", i, got.DirtyPages[i], tt.rec.DirtyPages[i])
				}
			}
		})
	}
}

// TestDecodeZeroCopy proves decoded OldData/NewData alias the source buffer:
// mutating the buffer after decoding is visible through the record.
func TestDecodeZeroCopy(t *testing.T) {
	rec := &Record{Type: TypeUpdate, TxnID: 1, PageID: 1, OldData: []byte("AAAA"), NewData: []byte("BBBB")}
	buf := rec.Encode()
	got, _, err := DecodeRecord(buf, CurrentVersion)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	oldOff := bytes.Index(buf, []byte("AAAA"))
	newOff := bytes.Index(buf, []byte("BBBB"))
	buf[oldOff] = 'Z'
	buf[newOff+3] = 'Y'
	if got.OldData[0] != 'Z' {
		t.Fatalf("OldData is a copy, not a zero-copy view: %q", got.OldData)
	}
	if got.NewData[3] != 'Y' {
		t.Fatalf("NewData is a copy, not a zero-copy view: %q", got.NewData)
	}
}

func TestDecodeBounds(t *testing.T) {
	valid := (&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, Offset: 0, OldData: []byte("ab"), NewData: []byte("cd")}).Encode()

	t.Run("empty", func(t *testing.T) {
		if _, _, err := DecodeRecord(nil, CurrentVersion); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("short-header", func(t *testing.T) {
		if _, _, err := DecodeRecord(valid[:recordHeaderSize-1], CurrentVersion); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("payload-overflow", func(t *testing.T) {
		buf := bytes.Clone(valid)
		binary.BigEndian.PutUint32(buf[1:5], 1<<30) // absurd payload length
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrPayloadOverflow) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("truncated-payload", func(t *testing.T) {
		for _, cut := range []int{1, 5, 10, 20, 31} {
			buf := bytes.Clone(valid)
			binary.BigEndian.PutUint32(buf[1:5], uint32(cut))
			if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
				t.Fatalf("cut=%d: got %v", cut, err)
			}
		}
	})
	t.Run("inner-length-overflow", func(t *testing.T) {
		buf := bytes.Clone(valid)
		binary.BigEndian.PutUint32(buf[1:5], uint32(len(buf)-recordHeaderSize))
		binary.BigEndian.PutUint32(buf[recordHeaderSize+24:], 1<<30) // oldLen
		binary.BigEndian.PutUint32(buf[recordHeaderSize+28:], 1<<30) // newLen
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("txn-payload-short", func(t *testing.T) {
		buf := make([]byte, recordHeaderSize+8)
		buf[0] = TypeCommit
		binary.BigEndian.PutUint32(buf[1:5], 8)
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("checkpoint-count-overflow", func(t *testing.T) {
		buf := make([]byte, recordHeaderSize+16)
		buf[0] = TypeEndCheckpoint
		binary.BigEndian.PutUint32(buf[1:5], 16)
		binary.BigEndian.PutUint32(buf[recordHeaderSize+8:], 1<<20) // txnCount
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("checkpoint-dpt-count-overflow", func(t *testing.T) {
		rec := &Record{Type: TypeEndCheckpoint, BeginLSN: 8, Txns: []TxnSnapshot{{TxnID: 1, LastLSN: 2}}}
		buf := rec.Encode()
		// Corrupt the dirty-page count at the end of the payload.
		binary.BigEndian.PutUint32(buf[len(buf)-4:], 1<<20)
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("clr-truncated-undonext", func(t *testing.T) {
		rec := &Record{Type: TypeCLR, TxnID: 1, PageID: 1, OldData: []byte("a"), NewData: []byte("b"), UndoNextLSN: 9}
		full := rec.Encode()
		// Keep only 3 bytes of the 8-byte undoNextLSN field.
		payloadLen := uint32(len(full) - recordHeaderSize - 5)
		buf := full[:recordHeaderSize+payloadLen]
		binary.BigEndian.PutUint32(buf[1:5], payloadLen)
		if _, _, err := DecodeRecord(buf, CurrentVersion); !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v", err)
		}
	})
	// Every truncation of a valid record must fail cleanly, never panic.
	t.Run("all-truncations-no-panic", func(t *testing.T) {
		for i := 0; i < len(valid); i++ {
			_, _, _ = DecodeRecord(valid[:i], CurrentVersion)
		}
	})
}

// TestForwardCompatTrailingBytes: a record written by a future version with
// extra payload fields must still decode; the extra bytes are skipped via
// the length prefix.
func TestForwardCompatTrailingBytes(t *testing.T) {
	rec := &Record{Type: TypeUpdate, TxnID: 5, PageID: 2, Offset: 4, OldData: []byte("x"), NewData: []byte("y")}
	base := rec.Encode()
	extra := []byte("future-field")
	future := make([]byte, 0, len(base)+len(extra))
	future = append(future, base...)
	future = append(future, extra...)
	binary.BigEndian.PutUint32(future[1:5], uint32(len(future)-recordHeaderSize))

	got, n, err := DecodeRecord(future, CurrentVersion)
	if err != nil {
		t.Fatalf("decode future record: %v", err)
	}
	if n != len(future) {
		t.Fatalf("consumed %d, want %d (incl. unknown suffix)", n, len(future))
	}
	if got.TxnID != 5 || !bytes.Equal(got.NewData, []byte("y")) {
		t.Fatalf("fields corrupted by forward-compat decode: %+v", got)
	}
}

// TestUnknownRecordTypeSkipped: scanners must skip record types they do not
// understand (written by newer versions) using the length prefix.
func TestUnknownRecordTypeSkipped(t *testing.T) {
	unknown := make([]byte, recordHeaderSize+13)
	unknown[0] = 0xF0 // future record type
	binary.BigEndian.PutUint32(unknown[1:5], 13)

	known := (&Record{Type: TypeCommit, TxnID: 9, PrevLSN: 1}).Encode()

	img := &WALImage{Data: append(unknown, known...), Version: CurrentVersion}
	var types []byte
	img.ScanFrom(0, func(rec *Record) bool {
		types = append(types, rec.Type)
		return true
	})
	if len(types) != 2 || types[0] != 0xF0 || types[1] != TypeCommit {
		t.Fatalf("scan over unknown type failed: %v", types)
	}
}

// TestBackwardCompatV1CLR: v1 CLR records have no undoNextLSN field; the
// decoder must default it to prevLSN.
func TestBackwardCompatV1CLR(t *testing.T) {
	// Hand-build a v1 CLR payload: 32-byte fixed part + old + new, no undoNext.
	old, new_ := []byte("oo"), []byte("nn")
	payload := make([]byte, 32+len(old)+len(new_))
	binary.BigEndian.PutUint64(payload[0:8], 77)   // txnID
	binary.BigEndian.PutUint64(payload[8:16], 123) // prevLSN
	binary.BigEndian.PutUint32(payload[16:20], 4)  // pageID
	binary.BigEndian.PutUint32(payload[24:28], uint32(len(old)))
	binary.BigEndian.PutUint32(payload[28:32], uint32(len(new_)))
	copy(payload[32:], old)
	copy(payload[32+len(old):], new_)

	buf := make([]byte, recordHeaderSize+len(payload))
	buf[0] = TypeCLR
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[recordHeaderSize:], payload)

	rec, _, err := DecodeRecord(buf, Version1)
	if err != nil {
		t.Fatalf("decode v1 CLR: %v", err)
	}
	if rec.UndoNextLSN != 123 {
		t.Fatalf("v1 CLR undoNextLSN: got %d, want prevLSN to chain (123)", rec.UndoNextLSN)
	}
	if !bytes.Equal(rec.OldData, old) || !bytes.Equal(rec.NewData, new_) {
		t.Fatalf("v1 CLR data mismatch")
	}
}

// FuzzDecodeRecord: decoding arbitrary bytes must never panic.
func FuzzDecodeRecord(f *testing.F) {
	f.Add((&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, OldData: []byte("a"), NewData: []byte("b")}).Encode())
	f.Add((&Record{Type: TypeCLR, TxnID: 2, PageID: 3, UndoNextLSN: 9}).Encode())
	f.Add((&Record{Type: TypeEndCheckpoint, BeginLSN: 8, Txns: []TxnSnapshot{{TxnID: 1}}}).Encode())
	f.Add([]byte{0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		rec, n, err := DecodeRecord(data, CurrentVersion)
		if err != nil {
			return
		}
		if n <= 0 || n > len(data) {
			t.Fatalf("invalid consumed length %d for buffer %d", n, len(data))
		}
		if rec.Type == TypeUpdate || rec.Type == TypeCLR {
			if len(rec.OldData) > len(data) || len(rec.NewData) > len(data) {
				t.Fatalf("decoded data larger than source buffer")
			}
		}
	})
}
