// Package store provides a crash-safe record store on top of the wire
// codec, plus fault injectors that simulate hostile conditions: sudden
// power loss, full disks and lossy networks. The store uses a write-ahead
// log with CRC32 checksums and commit markers so that any torn or
// incomplete write is rolled back during recovery.
package store

import (
	"errors"
	"hash/crc32"
	"sync"
	"sync/atomic"
)

// ErrDiskFull is injected (or genuinely returned) when a write cannot
// complete because the device reports no space.
var ErrDiskFull = errors.New("store: no space left on device (disk full)")

// ErrPowerLoss is returned to the caller whose write was interrupted by a
// simulated power cut. The store is left in a recoverable state.
var ErrPowerLoss = errors.New("store: simulated sudden power loss")

// ErrPacketLoss is returned when the lossy transport drops a frame.
var ErrPacketLoss = errors.New("store: simulated network packet loss")

// Storage is the minimal durable byte device the WAL needs. *os.File
// satisfies it.
type Storage interface {
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Truncate(size int64) error
	Sync() error
	Size() int64
}

// CrashStorage wraps a Storage and injects faults. All knobs are
// concurrency-safe and may be flipped while writers are active.
type CrashStorage struct {
	Inner Storage

	mu          sync.Mutex
	pendingCuts []int // byte budgets for upcoming writes; 0 = cut at random-ish point
	diskFull    int32 // atomic: when >0, writes fail with ErrDiskFull
}

// NewCrashStorage wraps inner with fault injection.
func NewCrashStorage(inner Storage) *CrashStorage {
	return &CrashStorage{Inner: inner}
}

// ArmPowerLoss schedules the next n writes to be torn: each write is cut
// after budget bytes (budget 0 means "immediately, 0 bytes land").
func (s *CrashStorage) ArmPowerLoss(budgets ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingCuts = append(s.pendingCuts, budgets...)
}

// SetDiskFull toggles the disk-full condition.
func (s *CrashStorage) SetDiskFull(full bool) {
	if full {
		atomic.StoreInt32(&s.diskFull, 1)
	} else {
		atomic.StoreInt32(&s.diskFull, 0)
	}
}

// WriteAt applies fault injection, then delegates.
func (s *CrashStorage) WriteAt(p []byte, off int64) (int, error) {
	if atomic.LoadInt32(&s.diskFull) != 0 {
		return 0, ErrDiskFull
	}
	s.mu.Lock()
	if len(s.pendingCuts) > 0 {
		budget := s.pendingCuts[0]
		s.pendingCuts = s.pendingCuts[1:]
		s.mu.Unlock()
		if budget >= len(p) {
			// The cut happens after this write fully landed.
			return s.Inner.WriteAt(p, off)
		}
		if budget > 0 {
			if _, err := s.Inner.WriteAt(p[:budget], off); err != nil {
				return 0, err
			}
		}
		return 0, ErrPowerLoss
	}
	s.mu.Unlock()
	return s.Inner.WriteAt(p, off)
}

func (s *CrashStorage) ReadAt(p []byte, off int64) (int, error) { return s.Inner.ReadAt(p, off) }
func (s *CrashStorage) Truncate(size int64) error               { return s.Inner.Truncate(size) }
func (s *CrashStorage) Sync() error                             { return s.Inner.Sync() }
func (s *CrashStorage) Size() int64                             { return s.Inner.Size() }

// LossyTransport simulates an unreliable datagram link: each Send has
// dropRate (0..1) probability of being lost, decided by a deterministic
// xorshift PRNG so tests are reproducible.
type LossyTransport struct {
	DropRate float64

	mu      sync.Mutex
	state   uint64
	sent    int32
	dropped int32
}

// NewLossyTransport creates a transport with the given drop probability and
// PRNG seed.
func NewLossyTransport(dropRate float64, seed uint64) *LossyTransport {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &LossyTransport{DropRate: dropRate, state: seed}
}

func (t *LossyTransport) next() float64 {
	t.state ^= t.state << 13
	t.state ^= t.state >> 7
	t.state ^= t.state << 17
	return float64(t.state>>11) / float64(1<<53)
}

// Send delivers frame to deliver, or drops it returning ErrPacketLoss.
func (t *LossyTransport) Send(frame []byte, deliver func([]byte) error) error {
	t.mu.Lock()
	roll := t.next()
	t.mu.Unlock()
	if roll < t.DropRate {
		atomic.AddInt32(&t.dropped, 1)
		return ErrPacketLoss
	}
	atomic.AddInt32(&t.sent, 1)
	return deliver(frame)
}

// Stats reports (delivered, dropped) frame counts.
func (t *LossyTransport) Stats() (sent, dropped int) {
	return int(atomic.LoadInt32(&t.sent)), int(atomic.LoadInt32(&t.dropped))
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func checksum(b []byte) uint32 { return crc32.Checksum(b, crcTable) }
