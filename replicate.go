package aries

import (
	"bytes"
	"fmt"
	"math/rand"
)

// ShipStats reports what happened during log shipping.
type ShipStats struct {
	Sent          int // total packet transmissions
	Dropped       int // packets lost to the faulty network
	Retransmitted int // packets re-sent after a gap was detected
}

// ShipLog replicates every record of src to dst over an unreliable
// connection that drops each packet with probability lossRate. The receiver
// detects gaps via LSN discontinuity and the sender retransmits from the
// first missing LSN until the replica is byte-identical to the source. This
// models a self-healing primary/replica log stream under packet loss.
func ShipLog(src, dst *WAL, lossRate float64, rnd *rand.Rand) (*ShipStats, error) {
	stats := &ShipStats{}
	recs, err := src.Scan()
	if err != nil {
		return nil, err
	}
	sentOnce := make(map[LSN]bool)
	const maxRounds = 100000
	for round := 0; round < maxRounds; round++ {
		if dst.Tail() >= src.Tail() {
			return stats, nil
		}
		for _, rec := range recs {
			if rec.LSN < dst.Tail() {
				continue // already replicated
			}
			stats.Sent++
			if sentOnce[rec.LSN] {
				stats.Retransmitted++
			}
			sentOnce[rec.LSN] = true
			if rnd.Float64() < lossRate {
				stats.Dropped++
				continue // packet lost in transit
			}
			if rec.LSN != dst.Tail() {
				// Gap detected: an earlier packet is missing. The receiver
				// rejects out-of-order data; the sender will retransmit.
				continue
			}
			if _, err := dst.Append(rec); err != nil {
				return stats, err
			}
		}
	}
	return stats, fmt.Errorf("aries: replication did not converge after %d rounds", maxRounds)
}

// WALBytes returns the raw bytes of the valid prefix of the WAL, used to
// verify byte-identical replication.
func WALBytes(w *WAL) ([]byte, error) {
	recs, err := w.Scan()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(walMagic)
	for _, rec := range recs {
		buf.Write(encodeRecord(rec))
	}
	return buf.Bytes(), nil
}
