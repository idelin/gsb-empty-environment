package ledger

import (
	"fmt"
	"io"
	"os"
)

// VerifyFile independently re-reads path and recomputes every balance
// and the whole hash chain from the raw bytes. It deliberately shares
// no state-transition code with the writer (Ledger.apply/commit), so an
// auditor gets a second implementation of the accounting rules.
//
// A trailing partial record is ignored (a crashed write may be dropped);
// a complete record whose hash or chain link is broken is reported with
// its byte offset via *CorruptError.
func VerifyFile(path string) (balances map[uint64]int64, count int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	return Verify(f)
}

// Verify is the io.Reader form of VerifyFile.
func Verify(r io.Reader) (map[uint64]int64, int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	balances := map[uint64]int64{}
	var prev [32]byte
	n := len(data) / RecordSize // incomplete tail bytes are ignored
	for i := 0; i < n; i++ {
		rec, decErr := DecodeRecord(data[i*RecordSize : (i+1)*RecordSize])
		if decErr != nil {
			return nil, i, &CorruptError{Offset: int64(i * RecordSize), Seq: uint64(i + 1)}
		}
		if rec.Seq != uint64(i+1) || rec.PrevHash != prev {
			return nil, i, &CorruptError{Offset: int64(i * RecordSize), Seq: uint64(i + 1)}
		}
		if rec.Amount <= 0 || rec.From == rec.To {
			return nil, i, fmt.Errorf("record seq %d: invalid transfer fields", rec.Seq)
		}
		balances[rec.From] -= rec.Amount
		balances[rec.To] += rec.Amount
		prev = rec.Hash
	}
	return balances, n, nil
}
