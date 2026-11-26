package threshold

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"sync"
)

// NonceCommitment is the public FROST round-one pair (D_i, E_i).
type NonceCommitment struct {
	ID     string `json:"id"`
	Signer ID     `json:"signer"`
	D      []byte `json:"d"`
	E      []byte `json:"e"`
}

// SigningCommitment is an alias for hosts using FROST terminology.
type SigningCommitment = NonceCommitment

// NonceBundle owns the private d/e scalars corresponding to one public
// (D,E) pair. Its Use method is the only way to access the secrets and it
// succeeds at most once, including under concurrent calls.
type NonceBundle struct {
	Commitment NonceCommitment
	scalarLen  int

	mu       sync.Mutex
	d        []byte
	e        []byte
	consumed bool
}

// Public returns a copy of the public commitment.
func (b *NonceBundle) Public() *NonceCommitment {
	if b == nil {
		return nil
	}
	return &NonceCommitment{
		ID:     b.Commitment.ID,
		Signer: b.Commitment.Signer,
		D:      append([]byte(nil), b.Commitment.D...),
		E:      append([]byte(nil), b.Commitment.E...),
	}
}

// Consumed reports whether the one-time private nonce has been destroyed.
func (b *NonceBundle) Consumed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consumed
}

// Use atomically marks the bundle spent and removes its stored secret before
// invoking fn. Concurrent or repeated callers receive ErrNonceReuse. Any
// error from fn does not restore the nonce; a new commitment must be generated.
func (b *NonceBundle) Use(fn func(d, e *big.Int) error) error {
	if b == nil {
		return fmt.Errorf("%w: nil nonce bundle", ErrUnknownNonce)
	}
	if fn == nil {
		return fmt.Errorf("%w: nil nonce callback", ErrInvalidConfig)
	}

	b.mu.Lock()
	if b.consumed {
		b.mu.Unlock()
		return fmt.Errorf("%w: id=%q signer=%q", ErrNonceReuse, b.Commitment.ID, b.Commitment.Signer)
	}
	b.consumed = true
	dBytes := b.d
	eBytes := b.e
	b.d = nil
	b.e = nil
	b.mu.Unlock()

	d := new(big.Int).SetBytes(dBytes)
	e := new(big.Int).SetBytes(eBytes)
	zeroBytes(dBytes)
	zeroBytes(eBytes)
	defer func() {
		d.SetUint64(0)
		e.SetUint64(0)
	}()

	return fn(d, e)
}

// Consume is terminology alias for Use: the bundle is marked spent and its
// stored private scalars are removed before the callback runs.
func (b *NonceBundle) Consume(fn func(d, e *big.Int) error) error {
	return b.Use(fn)
}

// Destroy atomically consumes and zeroes the bundle without producing a
// signature. It is useful for abort/cleanup paths.
func (b *NonceBundle) Destroy() error {
	return b.Use(func(_, _ *big.Int) error { return nil })
}

// NonceVault stores pre-generated one-time nonce bundles for one signer.
type NonceVault struct {
	group  Group
	signer ID
	rng    io.Reader

	mu      sync.Mutex
	bundles map[string]*NonceBundle
	counter uint64
}

func NewNonceVault(g Group, signer ID, rng io.Reader) *NonceVault {
	if rng == nil {
		rng = rand.Reader
	}
	return &NonceVault{group: g, signer: signer, rng: rng, bundles: make(map[string]*NonceBundle)}
}

// Generate creates and stores one random (d,e)/(D,E) pair.
func (v *NonceVault) Generate() (*NonceBundle, error) {
	if v == nil || v.group == nil {
		return nil, ErrGroupRequired
	}
	d, err := v.group.RandomScalar(v.rng)
	if err != nil {
		return nil, err
	}
	e, err := v.group.RandomScalar(v.rng)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	v.counter++
	randomID := make([]byte, 8)
	if _, err := io.ReadFull(v.rng, randomID); err != nil {
		v.mu.Unlock()
		return nil, err
	}
	id := fmt.Sprintf("%d-%d-%s", v.counter, v.scalarLenSafe(), hex.EncodeToString(randomID))
	bundle := &NonceBundle{
		Commitment: NonceCommitment{
			ID:     id,
			Signer: v.signer,
			D:      v.group.Encode(v.group.ScalarBaseMult(d)),
			E:      v.group.Encode(v.group.ScalarBaseMult(e)),
		},
		scalarLen: v.scalarLenSafe(),
		d:         encodeScalar(v.group, d),
		e:         encodeScalar(v.group, e),
	}
	v.bundles[id] = bundle
	v.mu.Unlock()

	d.SetUint64(0)
	e.SetUint64(0)
	return bundle, nil
}

func (v *NonceVault) scalarLenSafe() int {
	if v == nil || v.group == nil {
		return 0
	}
	return scalarByteLen(v.group)
}

// GenerateBatch creates n independent one-time bundles.
func (v *NonceVault) GenerateBatch(n int) ([]*NonceBundle, error) {
	if n < 0 {
		return nil, fmt.Errorf("%w: negative batch size", ErrInvalidConfig)
	}
	out := make([]*NonceBundle, 0, n)
	for i := 0; i < n; i++ {
		b, err := v.Generate()
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// Get returns a previously generated bundle without consuming it.
func (v *NonceVault) Get(id string) (*NonceBundle, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.bundles[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownNonce, id)
	}
	return b, nil
}

// Use finds a bundle and atomically consumes it.
func (v *NonceVault) Use(id string, fn func(d, e *big.Int) error) error {
	b, err := v.Get(id)
	if err != nil {
		return err
	}
	return b.Use(fn)
}

// Consume is an alias for Use.
func (v *NonceVault) Consume(id string, fn func(d, e *big.Int) error) error {
	return v.Use(id, fn)
}

// Destroy atomically consumes and zeroes one stored nonce.
func (v *NonceVault) Destroy(id string) error {
	b, err := v.Get(id)
	if err != nil {
		return err
	}
	return b.Destroy()
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
