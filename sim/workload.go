package sim

import (
	"bytes"
	"encoding/binary"

	"aries/db"
	"aries/storage"
)

// WorkloadConfig tunes the generated transaction workload.
type WorkloadConfig struct {
	NumTxns         int    // transactions to run
	MaxOpsPerTxn    int    // updates per transaction (1..N)
	CheckpointEvery uint64 // logical-time interval between checkpoints
	FlushProb       int    // percent chance of flushing a page per event
	AbortProb       int    // percent chance a transaction aborts
	CrashAt         uint64 // logical time of the crash (0 = no crash)
}

// pageFor assigns each transaction a disjoint page range, which
// models what strict 2PL would guarantee in a real system: two
// concurrent transactions never hold conflicting page locks.
func pageFor(txn uint32, op int, cfg *WorkloadConfig) uint32 {
	return txn*uint32(cfg.MaxOpsPerTxn+1) + uint32(op)
}

// Workload records the outcome of a simulated run for verification.
type Workload struct {
	// Committed maps page -> expected after-image from committed txns.
	Committed map[uint32][]byte
	// Initial maps page -> image before any transaction touched it.
	Initial map[uint32][]byte
	// AbortedTxns and CommittedTxns record txn outcomes.
	AbortedTxns   []uint32
	CommittedTxns []uint32
	Crashed       bool
}

// RunWorkload drives db with a deterministic random workload inside
// the simulation and returns the ground-truth model for verification.
func RunWorkload(s *Sim, database *db.DB, cfg WorkloadConfig) *Workload {
	w := &Workload{
		Committed: make(map[uint32][]byte),
		Initial:   make(map[uint32][]byte),
	}
	if cfg.MaxOpsPerTxn < 1 {
		cfg.MaxOpsPerTxn = 1
	}
	rng := s.RNG()

	pageValue := func(txn uint32, op int) []byte {
		// Deterministic, unique after-image per (txn, op).
		buf := make([]byte, storage.PageSize)
		binary.BigEndian.PutUint32(buf[0:], txn)
		binary.BigEndian.PutUint64(buf[8:], uint64(op))
		binary.BigEndian.PutUint64(buf[16:], uint64(rng.Int63()))
		return buf
	}

	// Checkpointing and random flushes are simulated background events.
	if cfg.CheckpointEvery > 0 {
		var ckpt func(s *Sim)
		ckpt = func(s *Sim) {
			if cfg.CrashAt == 0 || s.Now() < cfg.CrashAt {
				database.Checkpoint()
				s.Schedule(cfg.CheckpointEvery, ckpt)
			}
		}
		s.Schedule(cfg.CheckpointEvery, ckpt)
	}

	// Crash event: kills the buffer pool at the configured time.
	if cfg.CrashAt > 0 {
		s.Schedule(cfg.CrashAt, func(s *Sim) {
			database.Crash()
			w.Crashed = true
		})
	}

	// Schedule each transaction's operations as discrete events.
	// Transactions overlap on the timeline (a new transaction starts
	// every few ticks while others are still running), so the crash
	// leaves genuinely in-flight losers behind.
	pending := make(map[uint32]map[uint32][]byte) // txn -> page -> after-image
	var clock uint64
	for t := 1; t <= cfg.NumTxns; t++ {
		txn := uint32(t)
		ops := 1 + rng.Intn(cfg.MaxOpsPerTxn)
		abort := rng.Intn(100) < cfg.AbortProb
		base := clock
		clock += 2 // next transaction starts while this one runs
		pending[txn] = make(map[uint32][]byte)
		for op := 0; op < ops; op++ {
			op := op
			page := pageFor(txn, op, &cfg)
			value := pageValue(txn, op)
			s.Schedule(base+uint64(op), func(s *Sim) {
				if w.Crashed {
					return
				}
				if _, seen := w.Initial[page]; !seen {
					if p, ok := database.Disk.Read(page); ok {
						w.Initial[page] = p.Data
					} else {
						w.Initial[page] = make([]byte, storage.PageSize)
					}
				}
				database.Update(txn, page, value)
				pending[txn][page] = value
				if rng.Intn(100) < cfg.FlushProb {
					database.Flush(page)
				}
			})
		}
		s.Schedule(base+uint64(ops), func(s *Sim) {
			if w.Crashed {
				return
			}
			if abort {
				database.Abort(txn)
				w.AbortedTxns = append(w.AbortedTxns, txn)
			} else {
				database.Commit(txn)
				w.CommittedTxns = append(w.CommittedTxns, txn)
				for page, value := range pending[txn] {
					w.Committed[page] = value
				}
			}
			delete(pending, txn)
		})
	}
	return w
}

// VerifyAgainst checks that a (post-recovery) disk image matches the
// ground truth: committed effects present, everything else original.
func (w *Workload) VerifyAgainst(disk *storage.Disk) error {
	snap := disk.Snapshot()
	for page, want := range w.Committed {
		got, ok := snap[page]
		if !ok || !bytes.Equal(got.Data, want) {
			return &Mismatch{Page: page, What: "committed value missing or wrong"}
		}
	}
	for page, want := range w.Initial {
		if _, committed := w.Committed[page]; committed {
			continue
		}
		if got, ok := snap[page]; ok && !bytes.Equal(got.Data, want) {
			return &Mismatch{Page: page, What: "uncommitted/aborted value leaked to disk"}
		}
	}
	return nil
}

// Mismatch describes a verification failure.
type Mismatch struct {
	Page uint32
	What string
}

func (m *Mismatch) Error() string {
	return "sim: verification failed on page " + itoa(m.Page) + ": " + m.What
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
