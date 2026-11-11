package wireadmit

const (
	// DefaultNestingBudget is the default maximum message/group nesting
	// depth. The top-level message counts as depth 1.
	DefaultNestingBudget = 100
	// DefaultMaxMessageSize is 64 MiB, matching the common protobuf default.
	DefaultMaxMessageSize = 64 << 20
)

type config struct {
	maxDepth int
	maxSize  int
}

// Option tunes admission behaviour.
type Option func(*config)

// WithNestingBudget limits how deeply messages and groups may nest. A value
// smaller than 1 is rejected when Admit runs.
func WithNestingBudget(depth int) Option {
	return func(c *config) { c.maxDepth = depth }
}

// WithMaxMessageSize limits the accepted message size in bytes. A
// non-positive value is rejected when Admit runs.
func WithMaxMessageSize(bytes int) Option {
	return func(c *config) { c.maxSize = bytes }
}

func defaultConfig() config {
	return config{maxDepth: DefaultNestingBudget, maxSize: DefaultMaxMessageSize}
}

// Admit validates one complete protobuf message against schema. On success it
// returns a Report describing known fields in arrival order and the exact
// ranges of unknown fields. The input is never copied or mutated, so after a
// nil error the gateway can forward msg unchanged.
func Admit(schema *Schema, msg []byte, opts ...Option) (*Report, error) {
	if schema == nil {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "nil schema"}
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxDepth < 1 {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "nesting budget must be at least 1"}
	}
	if cfg.maxSize <= 0 {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "max message size must be positive"}
	}
	if len(msg) > cfg.maxSize {
		return nil, &Error{Code: ErrSize, Offset: -1,
			Msg: "message size " + itoa(len(msg)) + " exceeds limit " + itoa(cfg.maxSize)}
	}
	return newParser(cfg).parseMessage(msg, 1, schema, 0)
}

// StreamValidator accumulates a message that arrives in multiple network
// chunks and validates the assembled bytes once. It never interprets partial
// input: chunks are appended until Admit is called, which makes boundaries
// inside tags, varints or length-delimited payloads safe.
type StreamValidator struct {
	schema *Schema
	cfg    config
	buf    []byte
}

// NewStreamValidator creates a validator bound to schema and options. The
// options are fixed at construction so every chunk is checked against the
// same limits.
func NewStreamValidator(schema *Schema, opts ...Option) *StreamValidator {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &StreamValidator{schema: schema, cfg: cfg}
}

// Write appends one chunk. It fails with ErrSize before appending whenever the
// accumulated length would exceed the configured maximum, leaving previously
// buffered bytes intact.
func (v *StreamValidator) Write(chunk []byte) (int, error) {
	if v.cfg.maxSize > 0 && len(v.buf)+len(chunk) > v.cfg.maxSize {
		return 0, &Error{
			Code:   ErrSize,
			Offset: -1,
			Msg: "accumulated size " + itoa(len(v.buf)+len(chunk)) +
				" exceeds limit " + itoa(v.cfg.maxSize),
		}
	}
	v.buf = append(v.buf, chunk...)
	return len(chunk), nil
}

// Admit validates every chunk accumulated so far as one message. On success
// the buffered bytes are retained and available via Bytes for transparent
// forwarding; call Reset to reuse the validator.
func (v *StreamValidator) Admit() (*Report, error) {
	if v.schema == nil {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "nil schema"}
	}
	if v.cfg.maxDepth < 1 {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "nesting budget must be at least 1"}
	}
	if v.cfg.maxSize <= 0 {
		return nil, &Error{Code: ErrSchema, Offset: -1, Msg: "max message size must be positive"}
	}
	return newParser(v.cfg).parseMessage(v.buf, 1, v.schema, 0)
}

// Bytes returns the accumulated message bytes. The slice aliases internal
// storage and must not be modified; copy it if the caller needs to retain it
// past a Reset.
func (v *StreamValidator) Bytes() []byte {
	return v.buf
}

// Len returns the number of buffered bytes.
func (v *StreamValidator) Len() int {
	return len(v.buf)
}

// Reset drops buffered data so the validator can be reused for the next
// message. The schema and options are unchanged.
func (v *StreamValidator) Reset() {
	v.buf = v.buf[:0]
}
