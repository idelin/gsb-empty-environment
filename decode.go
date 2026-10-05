package wire

import (
	"errors"
	"io"
)

// TreeBuilder assembles a stream of parser Events into a Message tree.
// It is useful for consumers that drive the Parser incrementally.
type TreeBuilder struct {
	root  *Message
	stack []*Message
}

// NewTreeBuilder creates an empty builder.
func NewTreeBuilder() *TreeBuilder {
	root := &Message{}
	return &TreeBuilder{root: root, stack: []*Message{root}}
}

// Apply folds one event into the tree.
func (b *TreeBuilder) Apply(ev Event) error {
	top := b.stack[len(b.stack)-1]
	switch ev.Kind {
	case EventUint64:
		top.Fields = append(top.Fields, Field{Num: ev.Field, Wire: WireVarint, U64: ev.U64})
	case EventFixed64:
		top.Fields = append(top.Fields, Field{Num: ev.Field, Wire: WireFixed64, F64: ev.F64})
	case EventFixed32:
		top.Fields = append(top.Fields, Field{Num: ev.Field, Wire: WireFixed32, F32: ev.F32})
	case EventBytes:
		top.Fields = append(top.Fields, Field{Num: ev.Field, Wire: WireBytes, Kind: KindRaw, Raw: ev.Raw})
	case EventPacked:
		top.Fields = append(top.Fields, Field{
			Num: ev.Field, Wire: WireBytes, Kind: KindPacked,
			Packed: ev.Packed, PackedWire: ev.PackedWire,
		})
	case EventMessageStart:
		child := &Message{}
		top.Fields = append(top.Fields, Field{Num: ev.Field, Wire: WireBytes, Kind: KindMessage, Msg: child})
		b.stack = append(b.stack, child)
	case EventMessageEnd:
		if len(b.stack) == 1 {
			return errors.New("wire: unbalanced message end")
		}
		b.stack = b.stack[:len(b.stack)-1]
	}
	return nil
}

// Root returns the assembled top-level message.
func (b *TreeBuilder) Root() *Message { return b.root }

// Decode reads a complete message from r, parsing incrementally in
// chunkSize-byte reads, and returns the decoded tree plus parser
// resource statistics. chunkSize <= 0 uses a default of 32 KiB.
func Decode(r io.Reader, limits Limits, schema Schema, chunkSize int) (*Message, Stats, error) {
	if chunkSize <= 0 {
		chunkSize = 32 << 10
	}
	p := NewParser(limits, schema)
	tb := NewTreeBuilder()
	buf := make([]byte, chunkSize)

	for {
		ev, err := p.Next()
		if err == ErrNeedMore {
			n, rerr := r.Read(buf)
			if n > 0 {
				if ferr := p.Feed(buf[:n]); ferr != nil {
					return nil, p.Stats(), ferr
				}
			}
			if rerr == io.EOF {
				p.Finish()
				continue
			}
			if rerr != nil {
				return nil, p.Stats(), rerr
			}
			continue
		}
		if err == io.EOF {
			return tb.Root(), p.Stats(), nil
		}
		if err != nil {
			return nil, p.Stats(), err
		}
		if err := tb.Apply(ev); err != nil {
			return nil, p.Stats(), err
		}
	}
}
