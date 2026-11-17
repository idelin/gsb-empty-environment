package netsec

import (
	"crypto/cipher"
	"fmt"
	"sync"
)

type epochState struct {
	epoch   uint32
	aead    cipher.AEAD
	windows map[string]*replayWindow
}

// Receiver authenticates records for the current epoch and, after rotation,
// the immediately preceding epoch. The zero value is ready for use; InstallKey
// can initialize it.
type Receiver struct {
	mu       sync.Mutex
	current  *epochState
	previous *epochState
}

// NewReceiver creates a receiver whose current epoch is epoch.
func NewReceiver(epoch uint32, key []byte) (*Receiver, error) {
	receiver := &Receiver{}
	if err := receiver.InstallKey(epoch, key); err != nil {
		return nil, err
	}
	return receiver, nil
}

// InstallKey installs key. On an empty receiver it establishes the current
// epoch. Otherwise it has the same strictly increasing semantics as Rotate.
func (r *Receiver) InstallKey(epoch uint32, key []byte) error {
	return r.Rotate(epoch, key)
}

// Rotate makes epoch current. The former current epoch remains available until
// the next rotation or an explicit Retire. The former previous epoch is
// discarded. Rotating to the current epoch, or to an older epoch, fails and
// leaves all keys and replay windows unchanged.
func (r *Receiver) Rotate(epoch uint32, key []byte) error {
	if r == nil {
		return ErrEpochUnavailable
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.current != nil {
		if epoch == r.current.epoch {
			return ErrEpochExists
		}
		if epoch < r.current.epoch {
			return ErrEpochRollback
		}
	}

	state, err := newEpochState(epoch, key)
	if err != nil {
		return err
	}
	if r.current == nil {
		r.current = state
		return nil
	}

	r.previous = r.current
	r.current = state
	return nil
}

// Retire removes the named previous epoch. It rejects an active current epoch
// so that callers cannot accidentally leave the receiver without a current
// key. After Retire succeeds, records for epoch are rejected.
func (r *Receiver) Retire(epoch uint32) error {
	if r == nil {
		return ErrNoPreviousEpoch
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.current != nil && r.current.epoch == epoch {
		return ErrCannotRetireCurrent
	}
	if r.previous == nil || r.previous.epoch != epoch {
		return ErrNoPreviousEpoch
	}
	r.previous = nil
	return nil
}

// RetirePrevious retires the previous epoch, if one is active.
func (r *Receiver) RetirePrevious() error {
	if r == nil {
		return ErrNoPreviousEpoch
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.previous == nil {
		return ErrNoPreviousEpoch
	}
	r.previous = nil
	return nil
}

// Receive authenticates record and atomically consumes its sequence before
// invoking deliver. Exactly one concurrent Receive for a given connection,
// epoch, and sequence can consume the sequence. A delivery callback error does
// not roll consumption back.
//
// Authentication happens before replay state is changed, so forged records,
// including records with forged high sequence numbers, cannot advance the
// window. The callback runs without the receiver lock, so it may call Rotate,
// Retire, or Receive.
func (r *Receiver) Receive(record Record, deliver func([]byte) error) error {
	if deliver == nil {
		return ErrNilCallback
	}
	if r == nil {
		return ErrEpochUnavailable
	}

	connectionID := append([]byte(nil), record.ConnectionID...)
	aad, err := RecordAAD(connectionID, record.Epoch, record.Sequence)
	if err != nil {
		return err
	}
	nonce := RecordNonce(record.Epoch, record.Sequence)

	r.mu.Lock()
	state := r.epochForReceive(record.Epoch)
	if state == nil {
		r.mu.Unlock()
		return ErrEpochUnavailable
	}

	plaintext, err := state.aead.Open(nil, nonce[:], record.Ciphertext, aad)
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrAuthentication, err)
	}

	connectionKey := string(connectionID)
	window := state.windows[connectionKey]
	if window == nil {
		window = &replayWindow{}
		state.windows[connectionKey] = window
	}
	if err := window.accept(record.Sequence); err != nil {
		clear(plaintext)
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()

	if err := deliver(plaintext); err != nil {
		return fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	return nil
}

func (r *Receiver) epochForReceive(epoch uint32) *epochState {
	if r.current != nil && r.current.epoch == epoch {
		return r.current
	}
	if r.previous != nil && r.previous.epoch == epoch {
		return r.previous
	}
	return nil
}

func newEpochState(epoch uint32, key []byte) (*epochState, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &epochState{
		epoch:   epoch,
		aead:    aead,
		windows: make(map[string]*replayWindow),
	}, nil
}
