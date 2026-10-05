package configstore

import (
	"errors"
	"io/ioutil"
	"math"
	"os"
)

var (
	// ErrNoConfig is returned by Load when neither slot holds a valid
	// committed configuration.
	ErrNoConfig = errors.New("configstore: no valid configuration found")
	// ErrTooLarge is returned by Publish when the payload exceeds MaxPayload.
	ErrTooLarge = errors.New("configstore: payload exceeds MaxPayload")
	// ErrGenerationOverflow is returned by Publish when the current
	// generation is math.MaxUint64. The counter is explicitly exhausted
	// rather than wrapped: at one publish per second a uint64 lasts
	// ~5.8e11 years, so rejection is the verifiable rule.
	ErrGenerationOverflow = errors.New("configstore: generation counter exhausted")
)

// Backend abstracts the storage of one slot. Implementations must make
// WriteCommit durable only after WriteBody has completed; the store relies
// on the commit flag being the last byte to reach stable storage.
type Backend interface {
	// Read returns the current slot image, or nil if the slot is absent.
	Read() ([]byte, error)
	// WriteBody writes a full slot image whose commit flag is cleared.
	WriteBody(image []byte) error
	// WriteCommit sets the commit flag of a previously written body.
	WriteCommit() error
}

// Store is a configuration publication store over two slots.
type Store struct {
	slots [2]Backend
}

// NewStore creates a Store backed by the two given slot backends.
func NewStore(a, b Backend) *Store {
	return &Store{slots: [2]Backend{a, b}}
}

// readValid reads and validates one slot; any failure yields ok=false.
func readValid(b Backend) (Meta, []byte, bool) {
	data, err := b.Read()
	if err != nil || len(data) == 0 {
		return Meta{}, nil, false
	}
	meta, payload, err := ValidateSlot(data)
	if err != nil {
		return Meta{}, nil, false
	}
	return meta, payload, true
}

// Load returns the newest fully committed configuration. A slot with a
// higher generation that is torn, corrupted or uncommitted is ignored, so
// a failed publish never hides the previous configuration.
func (s *Store) Load() ([]byte, Meta, error) {
	best := -1
	var bestMeta Meta
	var bestPayload []byte
	for i := 0; i < 2; i++ {
		meta, payload, ok := readValid(s.slots[i])
		if !ok {
			continue
		}
		if best < 0 || meta.Generation > bestMeta.Generation {
			best, bestMeta, bestPayload = i, meta, payload
		}
	}
	if best < 0 {
		return nil, Meta{}, ErrNoConfig
	}
	out := make([]byte, len(bestPayload))
	copy(out, bestPayload)
	return out, bestMeta, nil
}

// Publish writes cfg as the next generation into the slot that does not
// hold the current newest valid configuration, committing it only after
// the body is fully written. On any failure the previously committed
// configuration remains readable.
func (s *Store) Publish(cfg []byte) (Meta, error) {
	if len(cfg) > MaxPayload {
		return Meta{}, ErrTooLarge
	}
	var metas [2]Meta
	var valid [2]bool
	for i := 0; i < 2; i++ {
		meta, _, ok := readValid(s.slots[i])
		if ok {
			metas[i], valid[i] = meta, true
		}
	}
	newest := -1
	var gen uint64
	for i := 0; i < 2; i++ {
		if valid[i] && (newest < 0 || metas[i].Generation > gen) {
			newest, gen = i, metas[i].Generation
		}
	}
	if valid[0] || valid[1] {
		if gen == math.MaxUint64 {
			return Meta{}, ErrGenerationOverflow
		}
	}
	target := 0
	if newest == 0 {
		target = 1
	}
	image := encodeSlot(gen+1, cfg)
	if err := s.slots[target].WriteBody(image); err != nil {
		return Meta{}, err
	}
	if err := s.slots[target].WriteCommit(); err != nil {
		return Meta{}, err
	}
	return Meta{Generation: gen + 1, Length: uint32(len(cfg))}, nil
}

// FileBackend stores a slot in a single file.
type FileBackend struct {
	Path string
}

// Read implements Backend.
func (f *FileBackend) Read() ([]byte, error) {
	data, err := ioutil.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

// WriteBody implements Backend. It refuses images that are already
// committed or have the wrong size.
func (f *FileBackend) WriteBody(image []byte) error {
	if len(image) != SlotSize {
		return ErrSlotSize
	}
	if image[CommitOffset] != 0 {
		return errors.New("configstore: body image must be uncommitted")
	}
	fh, err := os.OpenFile(f.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := fh.Write(image); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

// WriteCommit implements Backend. It verifies the body is complete before
// flagging it, so a torn body can never become committed by extending the
// file.
func (f *FileBackend) WriteCommit() error {
	fh, err := os.OpenFile(f.Path, os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	fi, err := fh.Stat()
	if err != nil {
		fh.Close()
		return err
	}
	if fi.Size() != SlotSize {
		fh.Close()
		return ErrSlotSize
	}
	if _, err := fh.WriteAt([]byte{committedByte}, CommitOffset); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}
