package netsec

import (
	"bytes"
	"context"
	"errors"
	"time"
)

const (
	// MaxFutureSkew is how far an STH timestamp may lead its receive time.
	MaxFutureSkew = 5 * time.Minute

	// RetentionWindowAfterMMD is the additional history accepted beyond the
	// log's maximum merge delay.
	RetentionWindowAfterMMD = 24 * time.Hour

	// DefaultMaxEntriesPerSourceLog bounds storage for a single source and log.
	DefaultMaxEntriesPerSourceLog = 32
)

// ErrSignatureVerification is wrapped around a verifier rejection.
var ErrSignatureVerification = errors.New("sth signature verification failed")

// STH is the signed tree head material accepted from a gossip source.
//
// LogID is carried by IngestRequest rather than STH because it selects the
// log configuration used for admission.
type STH struct {
	TreeSize  uint64
	Timestamp time.Time
	RootHash  []byte
	Signature []byte
}

// STHVerifier is supplied by the caller. It must authenticate the STH
// signature against the log identified by logID.
type STHVerifier interface {
	Verify(ctx context.Context, logID string, sth STH) error
}

// VerifyFunc adapts a function to STHVerifier.
type VerifyFunc func(ctx context.Context, logID string, sth STH) error

// Verify implements STHVerifier.
func (f VerifyFunc) Verify(ctx context.Context, logID string, sth STH) error {
	return f(ctx, logID, sth)
}

// StoredSTH is an STH retained by the ingestor together with its provenance.
type StoredSTH struct {
	LogID      string
	SourceID   string
	STH        STH
	ReceivedAt time.Time
	Sequence   uint64
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func cloneSTH(sth STH) STH {
	sth.RootHash = cloneBytes(sth.RootHash)
	sth.Signature = cloneBytes(sth.Signature)
	return sth
}

func cloneStored(stored StoredSTH) StoredSTH {
	stored.STH = cloneSTH(stored.STH)
	return stored
}

func sameSTH(a, b STH) bool {
	return a.Timestamp.Equal(b.Timestamp) &&
		a.TreeSize == b.TreeSize &&
		bytes.Equal(a.RootHash, b.RootHash) &&
		bytes.Equal(a.Signature, b.Signature)
}

// compareSTH orders STHs by timestamp and then tree size. Signature and root
// bytes are deliberately excluded: they describe different views at the same
// tree state and must not make an older STH look newer.
func compareSTH(a, b STH) int {
	switch {
	case a.Timestamp.Before(b.Timestamp):
		return -1
	case a.Timestamp.After(b.Timestamp):
		return 1
	}
	switch {
	case a.TreeSize < b.TreeSize:
		return -1
	case a.TreeSize > b.TreeSize:
		return 1
	default:
		return 0
	}
}

func compareStored(a, b StoredSTH) int {
	if cmp := compareSTH(a.STH, b.STH); cmp != 0 {
		return cmp
	}
	switch {
	case a.Sequence < b.Sequence:
		return -1
	case a.Sequence > b.Sequence:
		return 1
	default:
		return 0
	}
}
