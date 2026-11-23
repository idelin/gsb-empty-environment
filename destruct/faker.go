package destruct

import (
	"context"
	"crypto/ed25519"
	"sync"
	"time"
)

// FakeReplica is an in-memory Eraser used by tests and example wiring. It
// models a persistent key replica: erase is idempotent per (volume, txn),
// failures can be injected, and generation mismatches are simulated by
// rotating the key before an erase call.
type FakeReplica struct {
	mu sync.Mutex

	id         ReplicaID
	location   Location
	generation uint64
	priv       ed25519.PrivateKey

	erased     map[string]Confirmation
	failNext   int
	failErr    error
	eraseCount int
}

// NewFakeReplica creates a replica holding key generation gen.
func NewFakeReplica(id ReplicaID, location Location, gen uint64, priv ed25519.PrivateKey) *FakeReplica {
	return &FakeReplica{
		id:         id,
		location:   location,
		generation: gen,
		priv:       priv,
		erased:     map[string]Confirmation{},
	}
}

// RotateKey advances the in-memory generation without telling the volume
// record, which lets tests reproduce a generation-mismatch confirmation.
func (f *FakeReplica) RotateKey(gen uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation = gen
}

// FailNext makes the next n Erase calls fail with err.
func (f *FakeReplica) FailNext(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext += n
	f.failErr = err
}

// EraseCount returns how many erase calls have been served.
func (f *FakeReplica) EraseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eraseCount
}

// IsErased reports whether the (volume, txn) pair already has a confirmation.
func (f *FakeReplica) IsErased(v VolumeID, t TxnID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.erased[string(v)+"|"+string(t)]
	return ok
}

// Erase implements Eraser.
func (f *FakeReplica) Erase(_ context.Context, req EraseRequest) (*Confirmation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eraseCount++
	key := string(req.VolumeID) + "|" + string(req.TxnID)
	if conf, ok := f.erased[key]; ok {
		cp := cloneConfirmation(conf)
		return &cp, nil
	}
	if f.failNext > 0 {
		f.failNext--
		err := f.failErr
		return nil, err
	}
	gen := f.generation
	sig := SignConfirmation(f.priv, req.VolumeID, req.TxnID, f.id, f.location, gen)
	conf := Confirmation{
		VolumeID:   req.VolumeID,
		TxnID:      req.TxnID,
		ReplicaID:  f.id,
		Location:   f.location,
		Generation: gen,
		IssuedAt:   time.Now(),
		Signature:  sig,
	}
	// A response whose generation does not match the generation the
	// coordinator asked to erase is not an erasure of the requested key:
	// return it so the signature/generation validation can reject it, but do
	// not cache it as an idempotent success.
	if gen != req.Replica.Generation {
		cp := cloneConfirmation(conf)
		return &cp, nil
	}
	f.erased[key] = conf
	cp := cloneConfirmation(conf)
	return &cp, nil
}
