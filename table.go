package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// TableVersion identifies one immutable IRK table snapshot. Every successful
// mutation advances it, including changes to unrelated devices.
type TableVersion uint64

type tableSnapshot struct {
	version TableVersion
	records []*deviceEntry
	byID    map[DeviceID]*deviceEntry
}

type deviceEntry struct {
	record  DeviceRecord
	retired []retiredIRK
}

type retiredIRK struct {
	irk         IRK
	expiresAt   time.Time
	fingerprint [32]byte
}

func newTableSnapshot() *tableSnapshot {
	return &tableSnapshot{byID: make(map[DeviceID]*deviceEntry)}
}

func (s *tableSnapshot) clone() *tableSnapshot {
	next := &tableSnapshot{
		version: s.version,
		records: make([]*deviceEntry, 0, len(s.records)),
		byID:    make(map[DeviceID]*deviceEntry, len(s.byID)),
	}
	for _, entry := range s.records {
		copied := &deviceEntry{
			record:  entry.record.clone(),
			retired: append([]retiredIRK(nil), entry.retired...),
		}
		next.records = append(next.records, copied)
		next.byID[copied.record.DeviceID] = copied
	}
	return next
}

// TableSnapshot is a read-only view of one resolved table version. Records
// returned by this view are copies.
type TableSnapshot struct {
	snapshot *tableSnapshot
}

// Snapshot atomically loads the current table version.
func (r *Resolver) Snapshot() TableSnapshot {
	return TableSnapshot{snapshot: r.snapshot.Load()}
}

// Version returns the table version captured by the snapshot.
func (s TableSnapshot) Version() TableVersion {
	if s.snapshot == nil {
		return 0
	}
	return s.snapshot.version
}

// Record returns a copy of the current record for id.
func (s TableSnapshot) Record(id DeviceID) (DeviceRecord, bool) {
	if s.snapshot == nil {
		return DeviceRecord{}, false
	}
	entry, ok := s.snapshot.byID[id]
	if !ok {
		return DeviceRecord{}, false
	}
	return entry.record.clone(), true
}

// Records returns copies of all records, sorted by device ID.
func (s TableSnapshot) Records() []DeviceRecord {
	if s.snapshot == nil {
		return nil
	}
	records := make([]DeviceRecord, 0, len(s.snapshot.records))
	for _, entry := range s.snapshot.records {
		records = append(records, entry.record.clone())
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].DeviceID < records[j].DeviceID
	})
	return records
}

// TableVersion returns the current version.
func (r *Resolver) TableVersion() TableVersion {
	return r.snapshot.Load().version
}

// Record returns a copy of the current record for id.
func (r *Resolver) Record(id DeviceID) (DeviceRecord, bool) {
	return r.Snapshot().Record(id)
}

// AddDevice inserts a new record. The input is copied. A successful add
// advances the table version.
func (r *Resolver) AddDevice(record DeviceRecord) (TableVersion, error) {
	if err := record.validate(); err != nil {
		return 0, err
	}
	stored := record.clone()
	return r.mutate(func(next *tableSnapshot) error {
		if _, exists := next.byID[stored.DeviceID]; exists {
			return fmt.Errorf("%w: %s", ErrDeviceExists, stored.DeviceID)
		}
		entry := &deviceEntry{record: stored}
		next.records = append(next.records, entry)
		next.byID[stored.DeviceID] = entry
		return nil
	})
}

// RotateIRK atomically replaces a device's current IRK, advances its record
// generation, and advances the table version. When the resolver has a positive
// old-IRK retention policy, the previous IRK is retained for discovery only
// until that grace period expires.
func (r *Resolver) RotateIRK(id DeviceID, newIRK IRK) (TableVersion, RecordGeneration, error) {
	var generation RecordGeneration
	version, err := r.mutate(func(next *tableSnapshot) error {
		entry, ok := next.byID[id]
		if !ok {
			return fmt.Errorf("%w: %s", ErrDeviceNotFound, id)
		}
		if entry.record.State != RecordActive {
			return fmt.Errorf("%w: %s", ErrDeviceRevoked, id)
		}
		if newIRK == entry.record.IRK {
			return fmt.Errorf("%w: %s", ErrSameIRK, id)
		}
		if entry.record.Generation == RecordGeneration(^uint64(0)) {
			return ErrRecordGenerationOverflow
		}

		now := r.now()
		retained := make([]retiredIRK, 0, len(entry.retired)+1)
		if r.oldIRKRetention > 0 {
			for _, old := range entry.retired {
				if now.Before(old.expiresAt) {
					retained = append(retained, old)
				}
			}
			oldIRK := entry.record.IRK
			expiresAt := now.Add(r.oldIRKRetention)
			retained = append(retained, retiredIRK{
				irk:         oldIRK,
				expiresAt:   expiresAt,
				fingerprint: retiredCredentialFingerprint(oldIRK, expiresAt),
			})
		}

		entry.record.IRK = newIRK
		entry.record.Generation++
		entry.retired = retained
		generation = entry.record.Generation
		return nil
	})
	return version, generation, err
}

// RevokeDevice atomically marks a device revoked, advances its generation, and
// advances the table version. All retained IRKs are discarded immediately.
func (r *Resolver) RevokeDevice(id DeviceID) (TableVersion, error) {
	return r.mutate(func(next *tableSnapshot) error {
		entry, ok := next.byID[id]
		if !ok {
			return fmt.Errorf("%w: %s", ErrDeviceNotFound, id)
		}
		if entry.record.State == RecordRevoked {
			return fmt.Errorf("%w: %s", ErrDeviceRevoked, id)
		}
		if entry.record.Generation == RecordGeneration(^uint64(0)) {
			return ErrRecordGenerationOverflow
		}
		entry.record.State = RecordRevoked
		entry.record.Generation++
		entry.retired = nil
		return nil
	})
}

func (r *Resolver) mutate(fn func(next *tableSnapshot) error) (TableVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.snapshot.Load()
	next := current.clone()
	if err := fn(next); err != nil {
		return 0, err
	}
	if current.version == TableVersion(^uint64(0)) {
		return 0, ErrTableVersionOverflow
	}
	next.version = current.version + 1
	r.snapshot.Store(next)
	return next.version, nil
}

func retiredCredentialFingerprint(irk IRK, expiresAt time.Time) [32]byte {
	return credentialFingerprint("netsec/retired-irk/v1", irk, expiresAt)
}

func currentCredentialFingerprint(irk IRK) [32]byte {
	return credentialFingerprint("netsec/current-irk/v1", irk, time.Time{})
}

func credentialFingerprint(domain string, irk IRK, expiresAt time.Time) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(irk[:])
	if !expiresAt.IsZero() {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(expiresAt.UnixNano()))
		_, _ = hash.Write(encoded[:])
	}
	var fingerprint [32]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}
