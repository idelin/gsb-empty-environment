package chain

import "bytes"

// Builder constructs a correctly linked run of frames.
type Builder struct {
	nextSeq    uint64
	lastDigest []byte
	out        bytes.Buffer
}

// NewBuilder starts at the genesis checkpoint.
func NewBuilder() *Builder {
	b, err := NewBuilderAt(GenesisCheckpoint())
	if err != nil {
		panic(err)
	}
	return b
}

// NewBuilderAt continues a chain from cp; it is the producer-side mirror of
// Parser.NewParserAt for suffix generation.
func NewBuilderAt(cp Checkpoint) (*Builder, error) {
	d := cp.LastDigest
	if d == nil {
		d = ZeroDigest()
	}
	if len(d) != digestSize {
		return nil, ErrBadCheckpoint
	}
	return &Builder{
		nextSeq:    cp.Seq,
		lastDigest: append([]byte(nil), d...),
	}, nil
}

// Add encodes one frame carrying payload, links it to the previous frame,
// appends it to the output stream and returns it.
func (b *Builder) Add(payload []byte) Frame {
	f := Frame{
		Seq:        b.nextSeq,
		PrevDigest: append([]byte(nil), b.lastDigest...),
		Payload:    append([]byte(nil), payload...),
	}
	raw := f.Encode()
	b.out.Write(raw)
	b.lastDigest = f.Digest()
	b.nextSeq++
	return f
}

// Bytes returns the raw bytes of every frame added so far.
func (b *Builder) Bytes() []byte { return b.out.Bytes() }

// Checkpoint returns the builder's current chain position.
func (b *Builder) Checkpoint() Checkpoint {
	return Checkpoint{Seq: b.nextSeq, LastDigest: append([]byte(nil), b.lastDigest...)}
}
