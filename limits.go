package streamguard

// Limits contains bounded-resource limits for the incremental XML parser.
// Zero values are replaced by conservative defaults.
type Limits struct {
	// MaxBufferBytes bounds unconsumed incremental input.
	MaxBufferBytes int
	// MaxDepth bounds simultaneously open elements.
	MaxDepth int
	// MaxAttributes bounds attributes on one start tag.
	MaxAttributes int
	// MaxNameBytes bounds one XML/QName in bytes.
	MaxNameBytes int
	// MaxAttributeValueBytes bounds one decoded attribute value.
	MaxAttributeValueBytes int
	// MaxTextBytes bounds direct text inside one element.
	MaxTextBytes int
	// MaxCommentBytes bounds one XML comment body.
	MaxCommentBytes int
	// MaxCommentsPerFrame bounds comments associated with one stanza/burst.
	MaxCommentsPerFrame int
	// MaxProcessingInstBytes bounds one processing instruction.
	MaxProcessingInstBytes int
}

// DefaultLimits returns conservative limits suitable for control-plane XML.
func DefaultLimits() Limits {
	return Limits{
		MaxBufferBytes:         1 << 20,
		MaxDepth:               32,
		MaxAttributes:          32,
		MaxNameBytes:           256,
		MaxAttributeValueBytes: 16 << 10,
		MaxTextBytes:           256 << 10,
		MaxCommentBytes:        4 << 10,
		MaxCommentsPerFrame:    32,
		MaxProcessingInstBytes: 4 << 10,
	}
}

// Normalize fills unset limits with defaults.
func (l Limits) Normalize() Limits {
	d := DefaultLimits()
	if l.MaxBufferBytes <= 0 {
		l.MaxBufferBytes = d.MaxBufferBytes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxAttributes <= 0 {
		l.MaxAttributes = d.MaxAttributes
	}
	if l.MaxNameBytes <= 0 {
		l.MaxNameBytes = d.MaxNameBytes
	}
	if l.MaxAttributeValueBytes <= 0 {
		l.MaxAttributeValueBytes = d.MaxAttributeValueBytes
	}
	if l.MaxTextBytes <= 0 {
		l.MaxTextBytes = d.MaxTextBytes
	}
	if l.MaxCommentBytes <= 0 {
		l.MaxCommentBytes = d.MaxCommentBytes
	}
	if l.MaxCommentsPerFrame <= 0 {
		l.MaxCommentsPerFrame = d.MaxCommentsPerFrame
	}
	if l.MaxProcessingInstBytes <= 0 {
		l.MaxProcessingInstBytes = d.MaxProcessingInstBytes
	}
	return l
}

// Config configures a Guard/state machine. The zero value is normalized to
// secure defaults.
type Config struct {
	Limits Limits
}

// DefaultConfig returns the secure default configuration.
func DefaultConfig() Config {
	return Config{Limits: DefaultLimits()}
}

func (c Config) normalize() Config {
	c.Limits = c.Limits.Normalize()
	return c
}
