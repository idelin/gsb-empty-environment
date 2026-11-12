package taskpkg

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

// SideEffect is the operation host's action. It runs only after the witness
// confirmation has been cryptographically verified, the sequence consumed and
// the audit entry durably persisted.
type SideEffect func(ctx context.Context) error

// Clock abstracts timestamp generation, mainly for deterministic tests.
type Clock func() time.Time

// Config configures a Committer.
type Config struct {
	// LogPath is the path of the JSON-lines audit log on ordinary disk.
	LogPath string
	// Witness is the trusted remote confirmation signer.
	Witness Witness
	// Counter is the injected non-rollbackable monotonic counter.
	Counter MonotonicCounter
	// Clock defaults to time.Now.
	Clock Clock
}

// Committer serializes audit-guarded operations against one log and one
// counter.
type Committer struct {
	store   *Store
	witness Witness
	counter MonotonicCounter
	clock   Clock
	mu      sync.Mutex
}

// Open validates an existing audit log and cross-checks its head against the
// counter. It fails closed when the log is missing/tampered, the counter is
// unavailable, or an old local snapshot is presented alongside a counter that
// has already advanced.
func Open(ctx context.Context, cfg Config) (*Committer, error) {
	if cfg.Witness == nil || cfg.Counter == nil || cfg.LogPath == "" {
		return nil, errors.New("taskpkg: LogPath, Witness and Counter are required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	publicKey := cfg.Witness.PublicKey()
	store, err := OpenStore(cfg.LogPath, publicKey)
	if err != nil {
		return nil, err
	}

	next, err := cfg.Counter.Next()
	if err != nil {
		_ = store.Close()
		return nil, errors.Join(ErrCounterUnavailable, err)
	}
	if next != store.HighestSequence()+1 {
		_ = store.Close()
		return nil, errors.Join(ErrLogCounterDesync, errors.New("old log snapshot cannot reuse consumed sequences"))
	}

	return &Committer{
		store:   store,
		witness: cfg.Witness,
		counter: cfg.Counter,
		clock:   clock,
	}, nil
}

// Recover starts a fresh empty log. It requires a counter whose next sequence
// is 1; reusing a counter with consumed sequences is rejected so old
// confirmations can never be replayed against the new log.
func Recover(ctx context.Context, cfg Config) (*Committer, error) {
	if cfg.Witness == nil || cfg.Counter == nil || cfg.LogPath == "" {
		return nil, errors.New("taskpkg: LogPath, Witness and Counter are required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	next, err := cfg.Counter.Next()
	if err != nil {
		return nil, errors.Join(ErrCounterUnavailable, err)
	}
	if next != 1 {
		return nil, errors.Join(ErrLogCounterDesync, errors.New("recovery requires a fresh counter"))
	}
	store, err := RecoverStore(cfg.LogPath, cfg.Witness.PublicKey())
	if err != nil {
		return nil, err
	}
	return &Committer{
		store:   store,
		witness: cfg.Witness,
		counter: cfg.Counter,
		clock:   clock,
	}, nil
}

// Commit runs the full audit-guarded protocol and only then executes effect.
//
// Ordering guarantees:
//  1. counter.Next reserves the sequence; failure => zero side effect.
//  2. the witness signs; failure/partition => zero side effect, nothing is
//     consumed or persisted.
//  3. the Ed25519 signature and every bound field are verified locally.
//  4. counter.Consume marks the sequence non-rollbackably; failure => the
//     verified confirmation is discarded and there is zero side effect.
//  5. the entry is persisted atomically.
//  6. only then does effect run.
func (c *Committer) Commit(ctx context.Context, op Operation, effect SideEffect) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	seq, err := c.counter.Next()
	if err != nil {
		return errors.Join(ErrCounterUnavailable, err)
	}
	digest := OperationDigest(op)
	prior := c.store.LastCommitment()
	req := CommitmentRequest{
		Operation:       op,
		Sequence:        seq,
		PriorCommit:     prior,
		OperationDigest: digest,
	}

	confirmation, err := c.witness.RequestConfirmation(ctx, req)
	if err != nil {
		return err
	}
	if err := VerifyConfirmation(c.witness.PublicKey(), req, confirmation); err != nil {
		return err
	}

	// Consume before persisting the side-effect-enabling record. If this
	// fails we drop the confirmation entirely; a retry obtains a fresh one,
	// and no sequence is ever consumed twice.
	if err := c.counter.Consume(seq); err != nil {
		return err
	}

	entry := Entry{
		Sequence:        confirmation.Sequence,
		Subject:         op.Subject,
		Summary:         op.Summary,
		Payload:         append([]byte(nil), op.Payload...),
		OperationDigest: digest,
		PriorCommit:     prior,
		Signature:       confirmation.Signature,
		Commitment:      EntryCommitment(confirmation),
		Timestamp:       c.clock(),
	}
	if err := c.store.Append(entry); err != nil {
		return err
	}

	if effect != nil {
		if err := effect(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Entries exposes the verified audit entries for inspection.
func (c *Committer) Entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Entries()
}

// Close releases the underlying store.
func (c *Committer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Close()
}

// PublicKey re-exports the trusted verification key for tooling.
func (c *Committer) PublicKey() ed25519.PublicKey {
	return c.witness.PublicKey()
}
