package netsec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ErrInvalidConfiguration identifies invalid log or ingestor configuration.
var ErrInvalidConfiguration = errors.New("invalid sth ingestor configuration")

// IngestStatus describes the disposition of a submitted STH.
type IngestStatus string

const (
	IngestAccepted           IngestStatus = "accepted"
	IngestDuplicate          IngestStatus = "duplicate"
	IngestRejectedInvalid    IngestStatus = "rejected_invalid"
	IngestRejectedUnknownLog IngestStatus = "rejected_unknown_log"
	IngestRejectedExpired    IngestStatus = "rejected_expired"
	IngestRejectedFuture     IngestStatus = "rejected_future"
	IngestRejectedSignature  IngestStatus = "rejected_signature"
	IngestRejectedCapacity   IngestStatus = "rejected_capacity"
)

// Admitted reports whether the STH was newly stored.
func (s IngestStatus) Admitted() bool {
	return s == IngestAccepted
}

// IngestRequest is one STH received from a gossip source.
type IngestRequest struct {
	LogID    string
	SourceID string
	STH      STH

	// ReceivedAt is when the gossip receiver observed the STH. When zero, the
	// ingestor's clock is used.
	ReceivedAt time.Time
}

// IngestResult reports an admission decision and any same-source entry evicted
// to make room for it.
type IngestResult struct {
	Status  IngestStatus
	Evicted *StoredSTH
}

// LogConfig supplies log-specific admission parameters.
type LogConfig struct {
	ID  string
	MMD time.Duration
}

type logState struct {
	mmd     time.Duration
	sources map[string]*sourceBucket
}

type sourceBucket struct {
	// entries are ordered oldest to newest. Eviction always removes index 0.
	entries []StoredSTH
}

type ingestorConfig struct {
	maxPerSourceLog int
	now             func() time.Time
}

// Option configures an Ingestor.
type Option func(*ingestorConfig) error

// WithMaxEntriesPerSourceLog sets the per-source, per-log retention cap.
func WithMaxEntriesPerSourceLog(n int) Option {
	return func(cfg *ingestorConfig) error {
		if n <= 0 {
			return fmt.Errorf("%w: max entries per source/log must be positive", ErrInvalidConfiguration)
		}
		cfg.maxPerSourceLog = n
		return nil
	}
}

// WithClock injects the clock used when IngestRequest.ReceivedAt is zero.
func WithClock(now func() time.Time) Option {
	return func(cfg *ingestorConfig) error {
		if now == nil {
			return fmt.Errorf("%w: clock must not be nil", ErrInvalidConfiguration)
		}
		cfg.now = now
		return nil
	}
}

// Ingestor validates and retains STHs from gossip receivers. It is safe for
// concurrent use.
type Ingestor struct {
	mu       sync.RWMutex
	verifier STHVerifier
	cfg      ingestorConfig
	logs     map[string]*logState
	sequence uint64
}

// NewIngestor creates an STH ingestor. The verifier is mandatory; an STH is
// never stored before the verifier accepts it.
func NewIngestor(verifier STHVerifier, opts ...Option) (*Ingestor, error) {
	if verifier == nil {
		return nil, fmt.Errorf("%w: verifier must not be nil", ErrInvalidConfiguration)
	}
	if f, ok := verifier.(VerifyFunc); ok && f == nil {
		return nil, fmt.Errorf("%w: verifier must not be nil", ErrInvalidConfiguration)
	}

	cfg := ingestorConfig{
		maxPerSourceLog: DefaultMaxEntriesPerSourceLog,
		now:             time.Now,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidConfiguration)
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	return &Ingestor{
		verifier: verifier,
		cfg:      cfg,
		logs:     make(map[string]*logState),
		sequence: 1,
	}, nil
}

// RegisterLog configures or updates the MMD for a log. Register every accepted
// log ID before ingesting its STHs.
func (i *Ingestor) RegisterLog(logID string, mmd time.Duration) error {
	if logID == "" {
		return fmt.Errorf("%w: log ID must not be empty", ErrInvalidConfiguration)
	}
	if mmd < 0 {
		return fmt.Errorf("%w: MMD must not be negative", ErrInvalidConfiguration)
	}
	if mmd > time.Duration(math.MaxInt64)-RetentionWindowAfterMMD {
		return fmt.Errorf("%w: MMD is too large", ErrInvalidConfiguration)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	log, ok := i.logs[logID]
	if !ok {
		log = &logState{
			mmd:     mmd,
			sources: make(map[string]*sourceBucket),
		}
		i.logs[logID] = log
		return nil
	}
	log.mmd = mmd
	return nil
}

// RegisterLogConfig configures a log from a LogConfig.
func (i *Ingestor) RegisterLogConfig(cfg LogConfig) error {
	return i.RegisterLog(cfg.ID, cfg.MMD)
}

// IngestSTH is a convenience wrapper that uses the ingestor's clock for the
// receive time.
func (i *Ingestor) IngestSTH(ctx context.Context, sourceID, logID string, sth STH) (IngestResult, error) {
	return i.Ingest(ctx, IngestRequest{
		LogID:    logID,
		SourceID: sourceID,
		STH:      sth,
	})
}

// Ingest applies admission checks, verifies the signature, and only then
// mutates storage.
func (i *Ingestor) Ingest(ctx context.Context, req IngestRequest) (IngestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.LogID == "" || req.SourceID == "" {
		return IngestResult{Status: IngestRejectedInvalid}, nil
	}

	receivedAt := req.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = i.cfg.now()
	}

	// Snapshot the caller's data so later mutation cannot alter what is
	// verified or stored.
	sth := cloneSTH(req.STH)

	i.mu.RLock()
	log, known := i.logs[req.LogID]
	mmd := time.Duration(0)
	if known {
		mmd = log.mmd
	}
	i.mu.RUnlock()
	if !known {
		return IngestResult{Status: IngestRejectedUnknownLog}, nil
	}

	if status, ok := admissionStatus(sth.Timestamp, receivedAt, mmd); !ok {
		return IngestResult{Status: status}, nil
	}
	if err := ctx.Err(); err != nil {
		return IngestResult{Status: IngestRejectedInvalid}, err
	}

	// Verify a separate snapshot. Storage keeps the caller's snapshot only
	// after this succeeds, so verifier-side mutation cannot change stored data.
	if err := i.verifier.Verify(ctx, req.LogID, cloneSTH(sth)); err != nil {
		return IngestResult{Status: IngestRejectedSignature},
			fmt.Errorf("%w: %w", ErrSignatureVerification, err)
	}
	if err := ctx.Err(); err != nil {
		return IngestResult{Status: IngestRejectedInvalid}, err
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	log = i.logs[req.LogID]
	if log == nil {
		return IngestResult{Status: IngestRejectedUnknownLog}, nil
	}
	// MMD may have been updated while verification was in progress.
	if status, ok := admissionStatus(sth.Timestamp, receivedAt, log.mmd); !ok {
		return IngestResult{Status: status}, nil
	}

	bucket, ok := log.sources[req.SourceID]
	if !ok {
		bucket = &sourceBucket{}
		log.sources[req.SourceID] = bucket
	}

	for _, existing := range bucket.entries {
		if sameSTH(existing.STH, sth) {
			return IngestResult{Status: IngestDuplicate}, nil
		}
	}

	candidate := StoredSTH{
		LogID:      req.LogID,
		SourceID:   req.SourceID,
		STH:        sth,
		ReceivedAt: receivedAt,
	}

	// At capacity, an older candidate would immediately become the eviction
	// target. Reject it without touching the retained set, so a flood of stale
	// STHs cannot displace a newer one from the same source.
	if len(bucket.entries) >= i.cfg.maxPerSourceLog {
		oldest := bucket.entries[0]
		if compareSTH(candidate.STH, oldest.STH) < 0 {
			return IngestResult{Status: IngestRejectedCapacity}, nil
		}
	}

	candidate.Sequence = i.sequence
	i.sequence++

	insertAt := sort.Search(len(bucket.entries), func(j int) bool {
		return compareStored(bucket.entries[j], candidate) > 0
	})
	bucket.entries = append(bucket.entries, StoredSTH{})
	copy(bucket.entries[insertAt+1:], bucket.entries[insertAt:])
	bucket.entries[insertAt] = candidate

	var evicted *StoredSTH
	if len(bucket.entries) > i.cfg.maxPerSourceLog {
		evictedEntry := cloneStored(bucket.entries[0])
		evicted = &evictedEntry
		copy(bucket.entries, bucket.entries[1:])
		bucket.entries[len(bucket.entries)-1] = StoredSTH{}
		bucket.entries = bucket.entries[:len(bucket.entries)-1]
	}

	return IngestResult{
		Status:  IngestAccepted,
		Evicted: evicted,
	}, nil
}

func admissionStatus(ts, receivedAt time.Time, mmd time.Duration) (IngestStatus, bool) {
	oldest := receivedAt.Add(-(mmd + RetentionWindowAfterMMD))
	if ts.Before(oldest) {
		return IngestRejectedExpired, false
	}
	if ts.After(receivedAt.Add(MaxFutureSkew)) {
		return IngestRejectedFuture, false
	}
	return IngestAccepted, true
}

// Latest returns the newest retained STH for a log across all sources.
func (i *Ingestor) Latest(logID string) (StoredSTH, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return StoredSTH{}, false
	}

	var latest StoredSTH
	found := false
	for _, bucket := range log.sources {
		if len(bucket.entries) == 0 {
			continue
		}
		entry := bucket.entries[len(bucket.entries)-1]
		if !found || compareStored(entry, latest) > 0 {
			latest = entry
			found = true
		}
	}
	if !found {
		return StoredSTH{}, false
	}
	return cloneStored(latest), true
}

// LatestFromSource returns the newest retained STH for a source and log.
func (i *Ingestor) LatestFromSource(logID, sourceID string) (StoredSTH, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return StoredSTH{}, false
	}
	bucket := log.sources[sourceID]
	if bucket == nil || len(bucket.entries) == 0 {
		return StoredSTH{}, false
	}
	return cloneStored(bucket.entries[len(bucket.entries)-1]), true
}

// Entries returns all retained entries for a log, newest first.
func (i *Ingestor) Entries(logID string) []StoredSTH {
	i.mu.RLock()
	var entries []StoredSTH
	if log := i.logs[logID]; log != nil {
		for _, bucket := range log.sources {
			for _, entry := range bucket.entries {
				entries = append(entries, cloneStored(entry))
			}
		}
	}
	i.mu.RUnlock()

	sort.Slice(entries, func(a, b int) bool {
		return compareStored(entries[a], entries[b]) > 0
	})
	return entries
}

// EntriesFromSource returns a source's retained entries for a log, newest
// first.
func (i *Ingestor) EntriesFromSource(logID, sourceID string) []StoredSTH {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return nil
	}
	bucket := log.sources[sourceID]
	if bucket == nil {
		return nil
	}
	entries := make([]StoredSTH, len(bucket.entries))
	for j := range bucket.entries {
		entries[len(bucket.entries)-1-j] = cloneStored(bucket.entries[j])
	}
	return entries
}

// Len returns how many entries are retained for a source and log.
func (i *Ingestor) Len(logID, sourceID string) int {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return 0
	}
	bucket := log.sources[sourceID]
	if bucket == nil {
		return 0
	}
	return len(bucket.entries)
}

// Sources returns the source IDs currently retained for a log.
func (i *Ingestor) Sources(logID string) []string {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return nil
	}
	sources := make([]string, 0, len(log.sources))
	for sourceID, bucket := range log.sources {
		if len(bucket.entries) > 0 {
			sources = append(sources, sourceID)
		}
	}
	sort.Strings(sources)
	return sources
}

// LogMMD returns the configured MMD for a log.
func (i *Ingestor) LogMMD(logID string) (time.Duration, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	log := i.logs[logID]
	if log == nil {
		return 0, false
	}
	return log.mmd, true
}
