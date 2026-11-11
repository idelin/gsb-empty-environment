// Package replay implements an importable replay-rejection component for a Go
// authentication host.
//
// The caller is expected to finish all cryptographic validation of a service
// ticket first and only then submit the accepted ticket material to a Cache.
// Within the configured acceptance window a given ticket key (client
// principal, service principal, ticket digest, client time and microsecond
// sequence number) can be accepted at most once.
//
// Durability and ordering guarantees:
//   - An acceptance record is made durable before Accept returns success, so
//     authorization is only granted after the record is persisted.
//   - The persisted clock lower bound (the highest accepted client time) is
//     monotonic: a wall-clock rollback can never lower it.
//   - Requests whose client time is too far in the future are rejected without
//     touching storage, so a future timestamp can never advance the lower
//     bound.
//
// The component is storage agnostic: implementations of the Storage interface
// decide how records are locked, read and durably appended, which makes the
// write points injectable for power-loss testing.
package replay

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sentinel errors returned by Cache.Accept. Callers can use errors.Is to
// distinguish rejection reasons from storage failures.
var (
	// ErrReplay indicates that the exact ticket key has already been
	// accepted inside the acceptance window.
	ErrReplay = errors.New("replay: ticket has already been accepted")

	// ErrStale indicates that the client time is below the effective lower
	// bound: either older than the past window or below the persisted clock
	// lower bound.
	ErrStale = errors.New("replay: request is below the acceptance lower bound")

	// ErrFuture indicates that the client time is farther in the future than
	// the configured tolerance. No state is changed by this rejection.
	ErrFuture = errors.New("replay: client time is too far in the future")

	// ErrClosed indicates that the cache (or its storage) has been closed.
	ErrClosed = errors.New("replay: cache is closed")

	// ErrPowerLoss is returned by fault-injecting storage to simulate the
	// process losing power at a durable write point.
	ErrPowerLoss = errors.New("replay: simulated power loss during durable write")
)

const defaultWindow = 5 * time.Minute

// Clock returns the authenticator's view of the current time. Tests can inject
// a deterministic implementation.
type Clock interface {
	Now() time.Time
}

// WallClock reads the operating-system wall clock.
type WallClock struct{}

// Now implements Clock.
func (WallClock) Now() time.Time { return time.Now() }

// Key is the replay identity of a cryptographically validated ticket. Two
// submissions with identical keys are the same ticket presentation.
type Key struct {
	// ClientPrincipal is the authenticated client identity (for example a
	// Kerberos client principal).
	ClientPrincipal string

	// ServicePrincipal is the identity of the service the ticket was issued
	// for.
	ServicePrincipal string

	// TicketDigest is a collision-resistant digest of the validated ticket
	// (for example the SHA-256 of its DER encoding).
	TicketDigest []byte

	// ClientTime is the timestamp claimed by the client inside the ticket or
	// authenticator.
	ClientTime time.Time

	// Sequence is the client-supplied microsecond sequence number
	// (usec-style authenticator sequence) that disambiguates presentations
	// sharing the same client timestamp.
	Sequence uint32
}

// ID returns the stable, collision-resistant identifier of the key. All fields
// are length-prefixed before hashing so that distinct field boundaries can
// never collide.
func (k Key) ID() string {
	h := sha256.New()
	writeString := func(s string) {
		var lenbuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenbuf[:], uint64(len(s)))
		h.Write(lenbuf[:n])
		h.Write([]byte(s))
	}
	writeBytes := func(p []byte) {
		var lenbuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenbuf[:], uint64(len(p)))
		h.Write(lenbuf[:n])
		h.Write(p)
	}
	writeString(k.ClientPrincipal)
	writeString(k.ServicePrincipal)
	writeBytes(k.TicketDigest)

	var fixed [12]byte
	binary.BigEndian.PutUint64(fixed[:8], uint64(k.ClientTime.UnixNano()))
	binary.BigEndian.PutUint32(fixed[8:], k.Sequence)
	h.Write(fixed[:])

	return hex.EncodeToString(h.Sum(nil))
}

// Config configures a Cache.
type Config struct {
	// Storage is the durable record store. Required.
	Storage Storage

	// Clock supplies the host time. Defaults to WallClock when nil.
	Clock Clock

	// PastWindow is how far behind the host clock a client time may be and
	// still be considered. Zero defaults to five minutes.
	PastWindow time.Duration

	// FutureTolerance is how far ahead of the host clock a client time may be
	// and still be considered. Zero defaults to five minutes.
	FutureTolerance time.Duration
}

// Cache serializes ticket acceptances against a Storage. It is safe for
// concurrent use within a process; coordination across processes is the
// responsibility of the Storage implementation.
type Cache struct {
	storage         Storage
	clock           Clock
	pastWindow      time.Duration
	futureTolerance time.Duration
	closeStorage    bool

	mu     sync.Mutex
	closed bool
}

// New constructs a Cache from cfg.
func New(cfg Config) (*Cache, error) {
	if cfg.Storage == nil {
		return nil, errors.New("replay: Config.Storage is required")
	}
	if cfg.PastWindow < 0 || cfg.FutureTolerance < 0 {
		return nil, errors.New("replay: time windows must not be negative")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = WallClock{}
	}
	pastWindow := cfg.PastWindow
	if pastWindow == 0 {
		pastWindow = defaultWindow
	}
	futureTolerance := cfg.FutureTolerance
	if futureTolerance == 0 {
		futureTolerance = defaultWindow
	}
	return &Cache{
		storage:         cfg.Storage,
		clock:           clock,
		pastWindow:      pastWindow,
		futureTolerance: futureTolerance,
		closeStorage:    true,
	}, nil
}

// WithoutStorageClose returns a cache whose Close leaves the backing Storage
// open. Tests that model repeated process restarts over one injected store use
// this so a crashed cache handle does not destroy the shared durable state.
func (c *Cache) WithoutStorageClose() *Cache {
	c.closeStorage = false
	return c
}

// Accept validates one ticket presentation. It must be called only after the
// caller has completed cryptographic verification of the ticket.
//
// On success the acceptance record and the new clock lower bound have been
// appended durably before nil is returned. ErrReplay, ErrStale and ErrFuture
// are rejections; any other error is storage failure and the ticket must not
// be authorized.
func (c *Cache) Accept(ctx context.Context, key Key) error {
	if err := validateKey(key); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}

	now := c.clock.Now()
	clientTime := key.ClientTime

	// Far-future requests are rejected before storage is touched, so they can
	// never advance the persisted lower bound.
	if clientTime.After(now.Add(c.futureTolerance)) {
		return ErrFuture
	}

	if err := c.storage.Lock(ctx); err != nil {
		return fmt.Errorf("replay: acquire storage lock: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_ = c.storage.Unlock(ctx)
		}
	}()

	records, err := c.storage.ReadAll(ctx)
	if err != nil {
		return fmt.Errorf("replay: read acceptance log: %w", err)
	}
	state := buildState(records)

	lowerBound := now.Add(-c.pastWindow)
	if state.watermark.After(lowerBound) {
		// The persisted clock lower bound dominates the rolling window and is
		// never allowed to regress because of a wall-clock rollback.
		lowerBound = state.watermark
	}
	if clientTime.Before(lowerBound) {
		if _, ok := state.entries[key.ID()]; ok {
			return ErrReplay
		}
		return ErrStale
	}

	if _, ok := state.entries[key.ID()]; ok {
		// A power loss between the entry append and the watermark append
		// leaves an orphan entry as the last record of the log. Retrying the
		// same ticket repairs that exact window by writing the missing bound
		// confirmation; ordinary replays (a watermark already follows) append
		// nothing.
		if len(records) > 0 && records[len(records)-1].Kind == KindEntry {
			watermark := state.watermark
			if clientTime.After(watermark) {
				watermark = clientTime
			}
			watermarkRecord := WatermarkRecord{
				LowerBoundUnixNano: watermark.UnixNano(),
				UpdatedAtUnixNano:  now.UnixNano(),
			}
			if err := c.storage.Append(ctx, Record{Kind: KindWatermark, Watermark: &watermarkRecord}); err != nil {
				return err
			}
		}
		return ErrReplay
	}

	entry := EntryRecord{
		ID:                 key.ID(),
		ClientPrincipal:    key.ClientPrincipal,
		ServicePrincipal:   key.ServicePrincipal,
		TicketDigest:       append([]byte(nil), key.TicketDigest...),
		ClientTimeUnixNano: clientTime.UnixNano(),
		Sequence:           key.Sequence,
		AcceptedAtUnixNano: now.UnixNano(),
	}
	// Durable acceptance record first: authorization is only granted once this
	// write succeeds.
	if err := c.storage.Append(ctx, Record{Kind: KindEntry, Entry: &entry}); err != nil {
		return err
	}

	watermark := clientTime
	if state.watermark.After(watermark) {
		watermark = state.watermark
	}
	watermarkRecord := WatermarkRecord{
		LowerBoundUnixNano: watermark.UnixNano(),
		UpdatedAtUnixNano:  now.UnixNano(),
	}
	// The record is appended on every acceptance even when the numeric lower
	// bound did not move: each entry is then paired with an explicit, durable
	// confirmation that the bound did not regress.
	if err := c.storage.Append(ctx, Record{Kind: KindWatermark, Watermark: &watermarkRecord}); err != nil {
		return err
	}
	return nil
}

// Close releases the underlying storage.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.closeStorage {
		return c.storage.Close()
	}
	return nil
}

type logState struct {
	entries   map[string]EntryRecord
	watermark time.Time
}

func buildState(records []Record) logState {
	state := logState{entries: make(map[string]EntryRecord, len(records))}
	for _, rec := range records {
		switch rec.Kind {
		case KindEntry:
			if rec.Entry == nil {
				continue
			}
			state.entries[rec.Entry.ID] = *rec.Entry
			if t := time.Unix(0, rec.Entry.ClientTimeUnixNano); t.After(state.watermark) {
				state.watermark = t
			}
		case KindWatermark:
			if rec.Watermark == nil {
				continue
			}
			if t := time.Unix(0, rec.Watermark.LowerBoundUnixNano); t.After(state.watermark) {
				state.watermark = t
			}
		}
	}
	return state
}

func validateKey(key Key) error {
	var missing []string
	if key.ClientPrincipal == "" {
		missing = append(missing, "client principal")
	}
	if key.ServicePrincipal == "" {
		missing = append(missing, "service principal")
	}
	if len(key.TicketDigest) == 0 {
		missing = append(missing, "ticket digest")
	}
	if key.ClientTime.IsZero() {
		missing = append(missing, "client time")
	}
	if len(missing) > 0 {
		return fmt.Errorf("replay: key is missing %s", strings.Join(missing, ", "))
	}
	return nil
}
