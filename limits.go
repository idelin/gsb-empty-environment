package safeunpack

import (
	"io"
	"math"
)

const (
	DefaultMaxEntries         = 10_000
	DefaultMaxFileBytes       = 64 << 20
	DefaultMaxTotalBytes      = 256 << 20
	DefaultMaxArchiveBytes    = 512 << 20
	DefaultMaxCompressionRate = 200.0
	DefaultMaxPathBytes       = 4096
)

// Limits bounds the resources consumed by validation and virtual unpacking.
// A zero value is replaced by the corresponding default. A negative byte or
// ratio limit disables that limit.
type Limits struct {
	MaxEntries          int
	MaxFileBytes        int64
	MaxTotalBytes       int64
	MaxArchiveBytes     int64
	MaxCompressionRatio float64
	MaxPathBytes        int
}

// Config is an alias for Limits for callers that prefer configuration naming.
type Config = Limits

// DefaultLimits returns conservative defaults suitable for an admission gate.
func DefaultLimits() Limits {
	return Limits{
		MaxEntries:          DefaultMaxEntries,
		MaxFileBytes:        DefaultMaxFileBytes,
		MaxTotalBytes:       DefaultMaxTotalBytes,
		MaxArchiveBytes:     DefaultMaxArchiveBytes,
		MaxCompressionRatio: DefaultMaxCompressionRate,
		MaxPathBytes:        DefaultMaxPathBytes,
	}
}

// DefaultConfig is an alias for DefaultLimits.
func DefaultConfig() Limits { return DefaultLimits() }

func (l Limits) normalized() Limits {
	if l.MaxEntries == 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if l.MaxArchiveBytes == 0 {
		l.MaxArchiveBytes = DefaultMaxArchiveBytes
	}
	if l.MaxCompressionRatio == 0 {
		l.MaxCompressionRatio = DefaultMaxCompressionRate
	}
	if l.MaxPathBytes == 0 {
		l.MaxPathBytes = DefaultMaxPathBytes
	}
	return l
}

// Option configures a Scanner.
type Option func(*settings)

type settings struct {
	limits Limits
}

// WithLimits replaces all limits.
func WithLimits(l Limits) Option {
	return func(s *settings) { s.limits = l }
}

// WithMaxEntries sets the maximum number of physical archive records.
func WithMaxEntries(n int) Option {
	return func(s *settings) { s.limits.MaxEntries = n }
}

// WithMaxFileBytes sets the maximum decoded size of one archive member.
func WithMaxFileBytes(n int64) Option {
	return func(s *settings) { s.limits.MaxFileBytes = n }
}

// WithMaxTotalBytes sets the maximum cumulative decoded member bytes.
func WithMaxTotalBytes(n int64) Option {
	return func(s *settings) { s.limits.MaxTotalBytes = n }
}

// WithMaxArchiveBytes bounds the compressed ZIP image loaded for random access.
func WithMaxArchiveBytes(n int64) Option {
	return func(s *settings) { s.limits.MaxArchiveBytes = n }
}

// WithMaxCompressionRatio sets the maximum decoded/compressed ratio.
func WithMaxCompressionRatio(ratio float64) Option {
	return func(s *settings) { s.limits.MaxCompressionRatio = ratio }
}

// WithMaxPathBytes sets the maximum byte length of a normalized path or link
// target.
func WithMaxPathBytes(n int) Option {
	return func(s *settings) { s.limits.MaxPathBytes = n }
}

// Scanner is a reusable, stateless configuration for archive validation.
type Scanner struct {
	limits Limits
}

// Validator is an alias for Scanner.
type Validator = Scanner

// NewScanner constructs a Scanner. Each scan still uses a fresh virtual tree.
func NewScanner(opts ...Option) *Scanner {
	s := settings{limits: DefaultLimits()}
	for _, opt := range opts {
		if opt != nil {
			opt(&s)
		}
	}
	return &Scanner{limits: s.limits.normalized()}
}

// New is an alias for NewScanner.
func New(opts ...Option) *Scanner { return NewScanner(opts...) }

// Limits returns the effective limits used by the scanner.
func (s *Scanner) Limits() Limits {
	if s == nil {
		return DefaultLimits()
	}
	return s.limits
}

func readAll(r io.Reader, max int64) ([]byte, error) {
	if max < 0 || max == math.MaxInt64 {
		return io.ReadAll(r)
	}
	limited := &io.LimitedReader{R: r, N: max + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, wrapError(ErrLimitExceeded, "", 0, "archive image exceeds %d bytes", max)
	}
	return data, nil
}

func wrapError(kind error, path string, offset int64, format string, args ...any) error {
	detail := format
	if len(args) > 0 {
		detail = sprintf(format, args...)
	}
	return &Error{Kind: kind, Path: path, Offset: offset, Detail: detail}
}
