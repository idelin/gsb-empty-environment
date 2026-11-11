package signerpool

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrPoolClosed    = errors.New("signer pool is closed")
	ErrSelfTest      = errors.New("new key self-test failed")
	ErrNoSigner      = errors.New("no active signer")
	ErrStaleRotation = errors.New("rotation lost the publish race to a newer generation")
)

// Signature pairs a signature with the fixed key generation that produced it.
type Signature struct {
	Value      []byte
	Generation int
}

// lease is one installed key generation plus its in-flight reference count.
type lease struct {
	generation int
	handle     *Handle

	mu     sync.Mutex
	refs   int
	closed bool // retired: no new leases may be handed out
	done   chan struct{}
}

// retain reserves the handle for one operation. Retired leases reject new work.
func (l *lease) retain() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.refs++
	return true
}

// release drops one reference; the terminal release closes the done channel.
func (l *lease) release() {
	l.mu.Lock()
	l.refs--
	if l.refs < 0 {
		l.mu.Unlock()
		panic("signerpool: negative lease reference count")
	}
	if l.refs == 0 && l.closed {
		close(l.done)
	}
	l.mu.Unlock()
}

// retire marks the lease ineligible for new work and reports whether a
// reaper goroutine must wait for outstanding references to drain.
func (l *lease) retire() (done <-chan struct{}, pending bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.refs == 0 {
		close(l.done)
		return l.done, false
	}
	return l.done, true
}

// Pool keeps one current key generation for new signing while retired
// generations stay alive until every in-flight operation has finished.
type Pool struct {
	hsm *HSM

	mu      sync.RWMutex
	current *lease
	nextGen int
	closed  bool

	// verifiers lets signatures of retired generations be verified after
	// their hardware handles have been destroyed.
	verifiersMu sync.RWMutex
	verifiers   map[int]ed25519.PublicKey

	reapers sync.WaitGroup
}

// NewPool builds an empty pool backed by the given pseudo HSM.
func NewPool(hsm *HSM) *Pool {
	return &Pool{
		hsm:       hsm,
		nextGen:   1,
		verifiers: make(map[int]ed25519.PublicKey),
	}
}

// CurrentGeneration reports the installed generation (0 before first rotate).
func (p *Pool) CurrentGeneration() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.current == nil {
		return 0
	}
	return p.current.generation
}

// Rotate installs a freshly generated key generation. The new handle is only
// published after a sign/verify self-test; on failure it is destroyed and the
// previously installed generation remains in service (rollback). Concurrent
// rotations commit in self-test completion order: a candidate that is no
// longer the newest generation is destroyed with ErrStaleRotation rather than
// moving the installed generation backwards.
func (p *Pool) Rotate() (int, error) {
	// Allocate the generation number under the write lock so concurrent
	// rotations can never reuse or reorder generations.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, ErrPoolClosed
	}
	gen := p.nextGen
	p.nextGen++
	old := p.current
	p.mu.Unlock()

	handle, err := p.hsm.GenerateKey(gen)
	if err != nil {
		return 0, fmt.Errorf("rotate generation %d: %w", gen, err)
	}

	if err := selfTest(handle); err != nil {
		if destroyErr := handle.Destroy(); destroyErr != nil {
			return 0, errors.Join(err, destroyErr)
		}
		return 0, fmt.Errorf("%w: generation %d: %v", ErrSelfTest, gen, err)
	}

	fresh := &lease{
		generation: gen,
		handle:     handle,
		done:       make(chan struct{}),
	}

	p.verifiersMu.Lock()
	p.verifiers[gen] = handle.PublicKey()
	p.verifiersMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = handle.Destroy()
		return 0, ErrPoolClosed
	}
	if gen != p.nextGen-1 {
		// A newer generation was allocated while self-testing. Destroy this
		// candidate and keep the winner of the publish race.
		p.mu.Unlock()
		if err := handle.Destroy(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("%w: generation %d", ErrStaleRotation, gen)
	}
	p.current = fresh
	p.mu.Unlock()

	if old != nil {
		p.retire(old)
	}
	return gen, nil
}

// selfTest proves the new hardware handle can sign and that its own verifier
// accepts the signature and rejects tampered message material.
func selfTest(handle *Handle) error {
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return fmt.Errorf("self-test challenge: %w", err)
	}
	sig, err := handle.Sign(challenge)
	if err != nil {
		return fmt.Errorf("self-test sign: %w", err)
	}
	if !handle.Verify(challenge, sig) {
		return errors.New("self-test verify rejected valid signature")
	}
	tampered := append([]byte(nil), challenge...)
	tampered[0] ^= 0xFF
	if handle.Verify(tampered, sig) {
		return errors.New("self-test verify accepted tampered message")
	}
	return nil
}

// retire takes a generation out of service and schedules destruction once all
// operations still referencing it have released their handles.
func (p *Pool) retire(l *lease) {
	done, pending := l.retire()
	if !pending {
		if err := l.handle.Destroy(); err != nil {
			panic("signerpool: destroy of unreferenced lease failed: " + err.Error())
		}
		return
	}
	p.reapers.Add(1)
	go func() {
		defer p.reapers.Done()
		<-done
		if err := l.handle.Destroy(); err != nil {
			panic("signerpool: destroy of drained lease failed: " + err.Error())
		}
	}()
}

// Sign pins the key generation active at call time for the whole operation:
// a rotation racing with this call can neither swap its handle nor destroy it
// until the signature has been produced.
func (p *Pool) Sign(msg []byte) (Signature, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return Signature{}, ErrPoolClosed
	}
	l := p.current
	p.mu.RUnlock()
	if l == nil {
		return Signature{}, ErrNoSigner
	}
	if !l.retain() {
		// Extremely tight race with Close; retry through the pool state.
		return Signature{}, ErrPoolClosed
	}
	defer l.release()

	sig, err := l.handle.Sign(msg)
	if err != nil {
		return Signature{}, err
	}
	return Signature{Value: sig, Generation: l.generation}, nil
}

// Verify checks a signature against the public key of its pinned generation,
// including generations whose hardware handles have already been destroyed.
func (p *Pool) Verify(msg []byte, sig Signature) bool {
	p.verifiersMu.RLock()
	pub, ok := p.verifiers[sig.Generation]
	p.verifiersMu.RUnlock()
	if !ok {
		return false
	}
	return ed25519.Verify(pub, msg, sig.Value)
}

// Close shuts the pool down. The current generation is retired immediately and
// destroyed once in-flight work drains; Close is safe to call repeatedly and
// concurrent with Sign and Rotate.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.reapers.Wait()
		return nil
	}
	p.closed = true
	active := p.current
	p.current = nil
	p.mu.Unlock()

	if active != nil {
		p.retire(active)
	}
	p.reapers.Wait()
	return nil
}
