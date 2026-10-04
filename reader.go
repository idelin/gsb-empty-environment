package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
)

// Verify independently re-reads the ledger file, re-checks every
// record digest and the whole hash chain, and recomputes all balances
// from scratch. It deliberately does not use Ledger, replay, commit or
// any other writer-side state function, so it can serve as an
// independent audit of the file's contents.
//
// It returns the recomputed balances. A partial tail record (from an
// interrupted write) is ignored; a corrupt complete record is reported
// with its position via *CorruptError.
func Verify(path string) (map[string]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Field offsets within a record, matching the fixed wire layout.
	const (
		offAcctA  = 8 + 1 + ReqIDLen + 8
		offDeltaA = offAcctA + AcctLen
		offAcctB  = offDeltaA + 8
		offDeltaB = offAcctB + AcctLen
	)
	complete := len(data) / RecordSize // trailing partial bytes ignored
	balances := make(map[string]int64)
	var prev [DigestLen]byte
	for i := 0; i < complete; i++ {
		rec := data[i*RecordSize : (i+1)*RecordSize]
		seq := binary.BigEndian.Uint64(rec[0:8])
		if seq != uint64(i)+1 {
			return nil, &CorruptError{Offset: int64(i) * RecordSize, Seq: uint64(i) + 1,
				Reason: fmt.Sprintf("out-of-order seq %d", seq)}
		}
		header := rec[:headerSize]
		digest := sha256.Sum256(header)
		if string(digest[:]) != string(rec[headerSize:RecordSize]) {
			return nil, &CorruptError{Offset: int64(i) * RecordSize, Seq: seq,
				Reason: "record digest mismatch"}
		}
		var prevInRec [DigestLen]byte
		copy(prevInRec[:], rec[headerSize-DigestLen:headerSize])
		if prevInRec != prev {
			return nil, &CorruptError{Offset: int64(i) * RecordSize, Seq: seq,
				Reason: "previous-record digest does not match chain"}
		}
		prev = digest

		acctA := TrimString(rec[offAcctA : offAcctA+AcctLen])
		deltaA := int64(binary.BigEndian.Uint64(rec[offDeltaA : offDeltaA+8]))
		acctB := TrimString(rec[offAcctB : offAcctB+AcctLen])
		deltaB := int64(binary.BigEndian.Uint64(rec[offDeltaB : offDeltaB+8]))
		balances[acctA] += deltaA
		if balances[acctA] < 0 {
			return nil, fmt.Errorf("audit: account %q goes negative (%d) at seq %d",
				acctA, balances[acctA], seq)
		}
		if acctB != "" {
			balances[acctB] += deltaB
			if balances[acctB] < 0 {
				return nil, fmt.Errorf("audit: account %q goes negative (%d) at seq %d",
					acctB, balances[acctB], seq)
			}
		}
	}
	return balances, nil
}
