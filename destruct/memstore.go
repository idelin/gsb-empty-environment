package destruct

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
)

// MemDisk is the durable backing storage. It survives MemStore replacements,
// which simulates a process restart after a crash: create a new MemStore over
// the same MemDisk and every committed write is still there.
type MemDisk struct {
	mu            sync.Mutex
	volumes       map[VolumeID]*VolumeRecord
	leases        map[LeaseID]*LeaseRecord
	txns          map[TxnID]*DestroyTxn
	confirmations map[confirmationKey]*ConfirmationRecord
}

type confirmationKey struct {
	txn     TxnID
	replica ReplicaID
}

// NewMemDisk creates an empty durable disk.
func NewMemDisk() *MemDisk {
	return &MemDisk{
		volumes:       map[VolumeID]*VolumeRecord{},
		leases:        map[LeaseID]*LeaseRecord{},
		txns:          map[TxnID]*DestroyTxn{},
		confirmations: map[confirmationKey]*ConfirmationRecord{},
	}
}

// MemStore is an in-memory Store with per-record optimistic CAS.
type MemStore struct {
	disk *MemDisk
}

// NewMemStore opens a store over the given (possibly pre-populated) disk.
func NewMemStore(disk *MemDisk) *MemStore {
	return &MemStore{disk: disk}
}

func cloneReplicaSpec(r ReplicaSpec) ReplicaSpec {
	r.PublicKey = append([]byte(nil), r.PublicKey...)
	return r
}

func cloneReplicaMap(in map[ReplicaID]ReplicaSpec) map[ReplicaID]ReplicaSpec {
	out := make(map[ReplicaID]ReplicaSpec, len(in))
	for id, spec := range in {
		out[id] = cloneReplicaSpec(spec)
	}
	return out
}

func cloneVolume(v *VolumeRecord) *VolumeRecord {
	c := *v
	c.Replicas = cloneReplicaMap(v.Replicas)
	return &c
}

func cloneTxn(t *DestroyTxn) *DestroyTxn {
	c := *t
	c.Replicas = cloneReplicaMap(t.Replicas)
	return &c
}

func cloneConfirmation(c Confirmation) Confirmation {
	c.Signature = append([]byte(nil), c.Signature...)
	return c
}

func confirmationsEqual(a, b Confirmation) bool {
	return a.VolumeID == b.VolumeID &&
		a.TxnID == b.TxnID &&
		a.ReplicaID == b.ReplicaID &&
		a.Location == b.Location &&
		a.Generation == b.Generation &&
		a.IssuedAt.Equal(b.IssuedAt) &&
		bytes.Equal(a.Signature, b.Signature)
}

// CreateVolume implements Store.
func (s *MemStore) CreateVolume(_ context.Context, v *VolumeRecord) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	if _, exists := s.disk.volumes[v.ID]; exists {
		return fmt.Errorf("%w: volume %s", ErrConflict, v.ID)
	}
	if v.Version == 0 {
		v.Version = 1
	}
	s.disk.volumes[v.ID] = cloneVolume(v)
	return nil
}

// GetVolume implements Store.
func (s *MemStore) GetVolume(_ context.Context, id VolumeID) (*VolumeRecord, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	v, ok := s.disk.volumes[id]
	if !ok {
		return nil, fmt.Errorf("%w: volume %s", ErrNotFound, id)
	}
	return cloneVolume(v), nil
}

// CompareSwapVolume implements Store.
func (s *MemStore) CompareSwapVolume(_ context.Context, old, next *VolumeRecord) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	cur, ok := s.disk.volumes[old.ID]
	if !ok {
		return fmt.Errorf("%w: volume %s", ErrNotFound, old.ID)
	}
	if cur.Version != old.Version {
		return fmt.Errorf("%w: volume %s at version %d, caller had %d", ErrConflict, old.ID, cur.Version, old.Version)
	}
	if next.ID != old.ID {
		return fmt.Errorf("cannot change volume id during CAS")
	}
	next.Version = cur.Version + 1
	s.disk.volumes[old.ID] = cloneVolume(next)
	return nil
}

// CreateLease implements Store.
func (s *MemStore) CreateLease(_ context.Context, l *LeaseRecord) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	if _, exists := s.disk.leases[l.ID]; exists {
		return fmt.Errorf("%w: lease %s", ErrConflict, l.ID)
	}
	if l.Version == 0 {
		l.Version = 1
	}
	cp := *l
	s.disk.leases[l.ID] = &cp
	return nil
}

// GetLease implements Store.
func (s *MemStore) GetLease(_ context.Context, id LeaseID) (*LeaseRecord, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	l, ok := s.disk.leases[id]
	if !ok {
		return nil, fmt.Errorf("%w: lease %s", ErrNotFound, id)
	}
	cp := *l
	return &cp, nil
}

// ListLeases implements Store.
func (s *MemStore) ListLeases(_ context.Context, vol VolumeID) ([]*LeaseRecord, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	var out []*LeaseRecord
	for _, l := range s.disk.leases {
		if l.Volume == vol {
			cp := *l
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// CompareSwapLease implements Store.
func (s *MemStore) CompareSwapLease(_ context.Context, old, next *LeaseRecord) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	cur, ok := s.disk.leases[old.ID]
	if !ok {
		return fmt.Errorf("%w: lease %s", ErrNotFound, old.ID)
	}
	if cur.Version != old.Version {
		return fmt.Errorf("%w: lease %s at version %d, caller had %d", ErrConflict, old.ID, cur.Version, old.Version)
	}
	if next.ID != old.ID || next.Volume != old.Volume {
		return fmt.Errorf("cannot change lease identity during CAS")
	}
	next.Version = cur.Version + 1
	cp := *next
	s.disk.leases[old.ID] = &cp
	return nil
}

// CreateTxn implements Store.
func (s *MemStore) CreateTxn(_ context.Context, t *DestroyTxn) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	if _, exists := s.disk.txns[t.ID]; exists {
		return fmt.Errorf("%w: txn %s", ErrConflict, t.ID)
	}
	if t.Version == 0 {
		t.Version = 1
	}
	s.disk.txns[t.ID] = cloneTxn(t)
	return nil
}

// GetTxn implements Store.
func (s *MemStore) GetTxn(_ context.Context, id TxnID) (*DestroyTxn, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	t, ok := s.disk.txns[id]
	if !ok {
		return nil, fmt.Errorf("%w: txn %s", ErrNotFound, id)
	}
	return cloneTxn(t), nil
}

// CompareSwapTxn implements Store.
func (s *MemStore) CompareSwapTxn(_ context.Context, old, next *DestroyTxn) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	cur, ok := s.disk.txns[old.ID]
	if !ok {
		return fmt.Errorf("%w: txn %s", ErrNotFound, old.ID)
	}
	if cur.Version != old.Version {
		return fmt.Errorf("%w: txn %s at version %d, caller had %d", ErrConflict, old.ID, cur.Version, old.Version)
	}
	if next.ID != old.ID || next.Volume != old.Volume {
		return fmt.Errorf("cannot change txn identity during CAS")
	}
	next.Version = cur.Version + 1
	s.disk.txns[old.ID] = cloneTxn(next)
	return nil
}

// ListPendingTxns implements Store.
func (s *MemStore) ListPendingTxns(_ context.Context) ([]*DestroyTxn, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	var out []*DestroyTxn
	for _, t := range s.disk.txns {
		if t.State != TxnDestroyed {
			out = append(out, cloneTxn(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// PutConfirmation implements Store. It is an idempotent content-checked
// create: an identical write succeeds (retry after a lost response), a
// conflicting write for the same (txn, replica) returns ErrConflict.
func (s *MemStore) PutConfirmation(_ context.Context, r *ConfirmationRecord) error {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	key := confirmationKey{txn: r.Confirmation.TxnID, replica: r.Confirmation.ReplicaID}
	if existing, ok := s.disk.confirmations[key]; ok {
		if !confirmationsEqual(existing.Confirmation, r.Confirmation) {
			return fmt.Errorf("%w: confirmation for txn %s replica %s", ErrConflict, key.txn, key.replica)
		}
		return nil
	}
	cp := ConfirmationRecord{
		Confirmation: cloneConfirmation(r.Confirmation),
		ReceivedAt:   r.ReceivedAt,
	}
	s.disk.confirmations[key] = &cp
	return nil
}

// GetConfirmation implements Store.
func (s *MemStore) GetConfirmation(_ context.Context, txn TxnID, replica ReplicaID) (*ConfirmationRecord, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	r, ok := s.disk.confirmations[confirmationKey{txn: txn, replica: replica}]
	if !ok {
		return nil, fmt.Errorf("%w: confirmation txn %s replica %s", ErrNotFound, txn, replica)
	}
	cp := ConfirmationRecord{
		Confirmation: cloneConfirmation(r.Confirmation),
		ReceivedAt:   r.ReceivedAt,
	}
	return &cp, nil
}

// ListConfirmations implements Store.
func (s *MemStore) ListConfirmations(_ context.Context, txn TxnID) ([]*ConfirmationRecord, error) {
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	var out []*ConfirmationRecord
	for key, r := range s.disk.confirmations {
		if key.txn == txn {
			cp := ConfirmationRecord{
				Confirmation: cloneConfirmation(r.Confirmation),
				ReceivedAt:   r.ReceivedAt,
			}
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Confirmation.ReplicaID < out[j].Confirmation.ReplicaID
	})
	return out, nil
}
