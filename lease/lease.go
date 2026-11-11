// Package lease implements durable leasing of precomputed signing randomness
// (nonce slots) for an HSM-backed signing service.
//
// Each slot is bound to a key version and a unique sequence number. A slot
// may be returned to the pool only before it is handed to the hardware. The
// moment a lease is handed off, the slot is permanently consumed: even if
// the hardware call times out, the precomputed randomness must never be
// reused, because the HSM may have already produced a signature with it.
//
// All state transitions are appended to a fsynced journal under an
// inter-process file lock, so multiple service instances (processes or
// goroutines) can share one slot directory without double-issuing a slot,
// and a power loss is recovered conservatively: any slot that was leased or
// handed off at crash time is treated as consumed.
package lease

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

// KeyVersion identifies a versioned signing key. Slots are scoped per key
// version: a slot of key A can never be leased or returned for key B.
type KeyVersion string

var (
	// ErrNoSlots is returned by Acquire when the key version has no free slot.
	ErrNoSlots = errors.New("lease: no free slot for key version")
	// ErrLeaseHandedOff is returned when releasing a lease that was already
	// handed to the hardware. Such a slot is permanently consumed.
	ErrLeaseHandedOff = errors.New("lease: already handed to hardware, release forbidden")
	// ErrLeaseBindingMismatch is returned when a lease token does not match
	// the key version / slot binding recorded in the journal, e.g. an
	// attempted cross-key return.
	ErrLeaseBindingMismatch = errors.New("lease: key/slot binding mismatch")
	// ErrLeaseSettled is returned when operating on a lease that was already
	// released or otherwise unknown.
	ErrLeaseSettled = errors.New("lease: unknown or already settled lease")
)

type slotState string

const (
	slotFree     slotState = "free"
	slotLeased   slotState = "leased"
	slotHanded   slotState = "handed_off"
	slotConsumed slotState = "consumed"
)

type slotInfo struct {
	state   slotState
	leaseID string
}

// Lease is a capability token for one slot of one key version. It is plain
// data; every mutating operation revalidates it against the journal, so a
// forged or tampered token (wrong key, wrong slot, wrong ID) is rejected.
type Lease struct {
	ID   string
	Key  KeyVersion
	Slot uint64
}

// Stats reports slot counts per state for a key version.
type Stats struct {
	Free     int
	Leased   int
	Handed   int
	Consumed int
}

// Manager owns a slot directory shared by any number of instances.
type Manager struct {
	dir        string
	instanceID string

	mu    sync.Mutex
	lockf *os.File
	jf    *os.File

	slots map[KeyVersion]map[uint64]*slotInfo
}

// Open creates or recovers a Manager rooted at dir. Recovery replays the
// journal and permanently consumes every slot that was leased or handed off
// when the previous instance died, then journals those consumptions.
func Open(dir, instanceID string) (*Manager, error) {
	if instanceID == "" {
		return nil, errors.New("lease: instance ID required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lockf, err := os.OpenFile(filepath.Join(dir, "lease.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	jf, err := os.OpenFile(filepath.Join(dir, "lease.journal"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		lockf.Close()
		return nil, err
	}
	m := &Manager{
		dir:        dir,
		instanceID: instanceID,
		lockf:      lockf,
		jf:         jf,
		slots:      make(map[KeyVersion]map[uint64]*slotInfo),
	}
	if err := m.withLock(func() error { return m.recoverLocked() }); err != nil {
		lockf.Close()
		jf.Close()
		return nil, err
	}
	return m, nil
}

// Close releases the journal and lock files. It is not required for
// crash-safety; abandoning a Manager without Close simulates power loss.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err1 := m.jf.Close()
	err2 := m.lockf.Close()
	return errors.Join(err1, err2)
}

// Provision adds count fresh slots for key. It is safe to call from several
// instances; sequence numbers are allocated under the lock.
func (m *Manager) Provision(key KeyVersion, count int) error {
	if count <= 0 {
		return errors.New("lease: count must be positive")
	}
	return m.withLock(func() error {
		slots := m.slots[key]
		if slots == nil {
			slots = make(map[uint64]*slotInfo)
			m.slots[key] = slots
		}
		var maxSeq uint64
		for seq := range slots {
			if seq > maxSeq {
				maxSeq = seq
			}
		}
		for i := 0; i < count; i++ {
			maxSeq++
			if err := m.appendLocked(record{Op: opProvision, Key: key, Slot: maxSeq, Instance: m.instanceID}); err != nil {
				return err
			}
			slots[maxSeq] = &slotInfo{state: slotFree}
		}
		return nil
	})
}

// Acquire leases the lowest-numbered free slot for key. The lease is durable
// before Acquire returns: if the process dies now, recovery consumes the
// slot rather than risking reuse.
func (m *Manager) Acquire(key KeyVersion) (*Lease, error) {
	id, err := newLeaseID(m.instanceID)
	if err != nil {
		return nil, err
	}
	var leased *Lease
	err = m.withLock(func() error {
		slots := m.slots[key]
		seqs := make([]uint64, 0, len(slots))
		for seq := range slots {
			seqs = append(seqs, seq)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for _, seq := range seqs {
			if slots[seq].state == slotFree {
				rec := record{Op: opLease, Key: key, Slot: seq, LeaseID: id, Instance: m.instanceID}
				if err := m.appendLocked(rec); err != nil {
					return err
				}
				slots[seq] = &slotInfo{state: slotLeased, leaseID: id}
				leased = &Lease{ID: id, Key: key, Slot: seq}
				return nil
			}
		}
		return ErrNoSlots
	})
	if err != nil {
		return nil, err
	}
	return leased, nil
}

// Handoff marks the lease's precomputed randomness as delivered to the HSM.
// It is the point of no return: afterwards Release fails and the slot can
// only be consumed, whether the hardware answers, errors, or times out.
func (m *Manager) Handoff(l *Lease) error {
	return m.withLock(func() error {
		s, err := m.expectLocked(l, slotLeased)
		if err != nil {
			return err
		}
		if err := m.appendLocked(record{Op: opHandoff, Key: l.Key, Slot: l.Slot, LeaseID: l.ID, Instance: m.instanceID}); err != nil {
			return err
		}
		s.state = slotHanded
		return nil
	})
}

// Consume permanently retires the lease's slot. Call it after the hardware
// completes (in any order relative to other leases) and also on timeout or
// any post-handoff failure. Consume is idempotent.
func (m *Manager) Consume(l *Lease) error {
	return m.withLock(func() error {
		s, ok := m.lookupLocked(l)
		if !ok {
			return ErrLeaseBindingMismatch
		}
		if s.leaseID != l.ID {
			return ErrLeaseBindingMismatch
		}
		switch s.state {
		case slotConsumed:
			return nil
		case slotLeased, slotHanded:
			if err := m.appendLocked(record{Op: opConsume, Key: l.Key, Slot: l.Slot, LeaseID: l.ID, Instance: m.instanceID}); err != nil {
				return err
			}
			s.state = slotConsumed
			return nil
		default:
			return ErrLeaseSettled
		}
	})
}

// Release returns a lease's slot to the free pool. It is only legal before
// Handoff; afterwards the randomness may already be inside a hardware
// signature and the slot is permanently consumed instead.
func (m *Manager) Release(l *Lease) error {
	return m.withLock(func() error {
		s, ok := m.lookupLocked(l)
		if !ok {
			return ErrLeaseBindingMismatch
		}
		switch s.state {
		case slotHanded, slotConsumed:
			return ErrLeaseHandedOff
		case slotLeased:
			if s.leaseID != l.ID {
				return ErrLeaseBindingMismatch
			}
			if err := m.appendLocked(record{Op: opRelease, Key: l.Key, Slot: l.Slot, LeaseID: l.ID, Instance: m.instanceID}); err != nil {
				return err
			}
			s.state = slotFree
			s.leaseID = ""
			return nil
		default:
			// Free slot: the token matches no live lease on it, so this is
			// a forged, stale, or cross-key return attempt.
			return ErrLeaseBindingMismatch
		}
	})
}

// Stats reports per-state slot counts for key.
func (m *Manager) Stats(key KeyVersion) Stats {
	var st Stats
	m.withLock(func() error { //nolint:errcheck // read-only
		for _, s := range m.slots[key] {
			switch s.state {
			case slotFree:
				st.Free++
			case slotLeased:
				st.Leased++
			case slotHanded:
				st.Handed++
			case slotConsumed:
				st.Consumed++
			}
		}
		return nil
	})
	return st
}

// lookupLocked finds the journal-recorded slot for the lease's key+slot and
// reports whether the token's binding is valid.
func (m *Manager) lookupLocked(l *Lease) (*slotInfo, bool) {
	if l == nil {
		return nil, false
	}
	slots, ok := m.slots[l.Key]
	if !ok {
		return nil, false
	}
	s, ok := slots[l.Slot]
	if !ok {
		return nil, false
	}
	return s, true
}

func (m *Manager) expectLocked(l *Lease, want slotState) (*slotInfo, error) {
	s, ok := m.lookupLocked(l)
	if !ok {
		return nil, ErrLeaseBindingMismatch
	}
	if s.leaseID != l.ID {
		return nil, ErrLeaseBindingMismatch
	}
	if s.state != want {
		if s.state == slotHanded || s.state == slotConsumed {
			return nil, ErrLeaseHandedOff
		}
		return nil, ErrLeaseSettled
	}
	return s, nil
}

// withLock serializes against other processes (flock) and other goroutines
// (mutex), then reloads the journal so cross-instance changes are visible.
func (m *Manager) withLock(fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := syscall.Flock(int(m.lockf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lease: lock: %w", err)
	}
	defer syscall.Flock(int(m.lockf.Fd()), syscall.LOCK_UN)
	if err := m.loadLocked(); err != nil {
		return err
	}
	return fn()
}

// loadLocked rebuilds in-memory state by replaying the journal. Records that
// fail validation (e.g. a forged cross-key release) are ignored.
func (m *Manager) loadLocked() error {
	if _, err := m.jf.Seek(0, 0); err != nil {
		return err
	}
	slots := make(map[KeyVersion]map[uint64]*slotInfo)
	get := func(key KeyVersion, seq uint64) (*slotInfo, bool) {
		ks, ok := slots[key]
		if !ok {
			return nil, false
		}
		s, ok := ks[seq]
		return s, ok
	}
	dec := json.NewDecoder(m.jf)
	var lastGood int64
	for {
		var rec record
		if err := dec.Decode(&rec); err != nil {
			if err != io.EOF {
				// Torn tail record from a crash mid-write: drop it so
				// future appends remain replayable.
				if terr := m.jf.Truncate(lastGood); terr != nil {
					return terr
				}
			}
			break
		}
		lastGood = dec.InputOffset()
		if slots[rec.Key] == nil {
			slots[rec.Key] = make(map[uint64]*slotInfo)
		}
		switch rec.Op {
		case opProvision:
			if _, ok := get(rec.Key, rec.Slot); !ok {
				slots[rec.Key][rec.Slot] = &slotInfo{state: slotFree}
			}
		case opLease:
			if s, ok := get(rec.Key, rec.Slot); ok && s.state == slotFree {
				s.state = slotLeased
				s.leaseID = rec.LeaseID
			}
		case opRelease:
			if s, ok := get(rec.Key, rec.Slot); ok && s.state == slotLeased && s.leaseID == rec.LeaseID {
				s.state = slotFree
				s.leaseID = ""
			}
		case opHandoff:
			if s, ok := get(rec.Key, rec.Slot); ok && s.state == slotLeased && s.leaseID == rec.LeaseID {
				s.state = slotHanded
			}
		case opConsume:
			if s, ok := get(rec.Key, rec.Slot); ok &&
				(s.state == slotLeased || s.state == slotHanded) && s.leaseID == rec.LeaseID {
				s.state = slotConsumed
			}
		}
	}
	m.slots = slots
	// Return to the end so subsequent appends do not overwrite replayed data.
	if _, err := m.jf.Seek(0, 2); err != nil {
		return err
	}
	return nil
}

// recoverLocked consumes every slot left leased or handed off by a dead
// instance and journals the consumption so the decision itself is durable.
func (m *Manager) recoverLocked() error {
	for key, slots := range m.slots {
		seqs := make([]uint64, 0, len(slots))
		for seq := range slots {
			seqs = append(seqs, seq)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for _, seq := range seqs {
			s := slots[seq]
			if s.state == slotLeased || s.state == slotHanded {
				rec := record{Op: opConsume, Key: key, Slot: seq, LeaseID: s.leaseID, Instance: m.instanceID}
				if err := m.appendLocked(rec); err != nil {
					return err
				}
				s.state = slotConsumed
			}
		}
	}
	return nil
}

// appendLocked writes one journal record and fsyncs before returning, so a
// state transition is durable before any caller can act on it.
func (m *Manager) appendLocked(rec record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := m.jf.Write(append(data, '\n')); err != nil {
		return err
	}
	return m.jf.Sync()
}

func newLeaseID(instanceID string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return instanceID + "-" + hex.EncodeToString(buf[:]), nil
}
