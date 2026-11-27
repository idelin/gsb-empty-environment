package streamguard

import "errors"

// FrameKind is the protocol-level classification of one XML frame.
type FrameKind string

const (
	FrameUnknown      FrameKind = "Unknown"
	FrameStreamOpen   FrameKind = "StreamOpen"
	FrameStreamClose  FrameKind = "StreamClose"
	FrameStartTLS     FrameKind = "StartTLS"
	FrameSASLAuth     FrameKind = "SASLAuth"
	FrameResourceBind FrameKind = "ResourceBind"
	FrameStanza       FrameKind = "Stanza"
)

// Common aliases for concise call sites.
const (
	FrameAuth = FrameSASLAuth
	FrameBind = FrameResourceBind
)

// Element is a completely parsed XML element.
type Element struct {
	Name     Name
	Attrs    []Attr
	Text     string
	Children []*Element
}

// Attr returns the first attribute matching a local or qualified name.
func (e *Element) Attr(name string) string {
	if e == nil {
		return ""
	}
	for _, attr := range e.Attrs {
		if attr.Name.Local == name || attr.Name.Raw == name {
			return attr.Value
		}
	}
	return ""
}

// Child returns the first direct child with the given local name.
func (e *Element) Child(local string) *Element {
	if e == nil {
		return nil
	}
	for _, child := range e.Children {
		if child.Name.Local == local {
			return child
		}
	}
	return nil
}

// HasChild reports whether a direct child exists.
func (e *Element) HasChild(local string) bool {
	return e.Child(local) != nil
}

// Frame is one stream header, complete child element, or stream close.
type Frame struct {
	Kind FrameKind
	*Element
}

// Type returns the frame kind.
func (f *Frame) Type() FrameKind { return f.Kind }

// FrameReader incrementally assembles complete XMPP-style frames. It leaves
// bytes belonging to later frames in its internal buffer.
type FrameReader struct {
	limits       Limits
	parser       *Parser
	stack        []*Element
	commentCount int
}

// NewFrameReader creates a frame reader.
func NewFrameReader(limits ...Limits) *FrameReader {
	cfg := DefaultLimits()
	if len(limits) > 0 {
		cfg = limits[0].Normalize()
	}
	return &FrameReader{limits: cfg, parser: NewParser(cfg)}
}

// Write appends raw bytes.
func (r *FrameReader) Write(data []byte) (int, error) {
	return r.parser.Write(data)
}

// Buffered returns unconsumed raw bytes.
func (r *FrameReader) Buffered() int { return r.parser.Buffered() }

// Reset discards parser state and all unconsumed bytes.
func (r *FrameReader) Reset() {
	r.parser.Reset()
	r.stack = nil
	r.commentCount = 0
}

// NextFrame returns the next complete frame or ErrNeedMore.
func (r *FrameReader) NextFrame() (*Frame, error) {
	for {
		event, err := r.parser.Next()
		if err != nil {
			return nil, err
		}

		switch event.Kind {
		case EventStartElement:
			element := &Element{
				Name:  event.Name,
				Attrs: event.Attrs,
			}

			if len(r.stack) == 0 {
				r.stack = append(r.stack, element)
				r.commentCount = 0
				return &Frame{Kind: FrameStreamOpen, Element: element}, nil
			}

			parent := r.stack[len(r.stack)-1]
			parent.Children = append(parent.Children, element)

			if event.SelfClosing {
				if len(r.stack) == 1 {
					r.commentCount = 0
					return &Frame{Kind: classifyFrame(element), Element: element}, nil
				}
				continue
			}
			r.stack = append(r.stack, element)

		case EventEndElement:
			if len(r.stack) == 0 {
				return nil, ErrMalformedXML
			}
			completed := r.stack[len(r.stack)-1]
			r.stack = r.stack[:len(r.stack)-1]

			if len(r.stack) == 0 {
				return &Frame{Kind: FrameStreamClose, Element: completed}, nil
			}
			if len(r.stack) == 1 {
				r.commentCount = 0
				return &Frame{Kind: classifyFrame(completed), Element: completed}, nil
			}

		case EventCharData:
			if len(r.stack) == 0 {
				continue
			}
			current := r.stack[len(r.stack)-1]
			if len(current.Text)+len(event.Text) > r.limits.MaxTextBytes {
				return nil, limitError(
					ErrTextLimit,
					r.limits.MaxTextBytes,
					len(current.Text)+len(event.Text),
				)
			}
			current.Text += event.Text

		case EventComment:
			r.commentCount++
			if r.commentCount > r.limits.MaxCommentsPerFrame {
				return nil, limitError(
					ErrCommentBomb,
					r.limits.MaxCommentsPerFrame,
					r.commentCount,
				)
			}

		case EventProcInst:
			// XML declarations and application PIs do not form frames.
		}
	}
}

func classifyFrame(element *Element) FrameKind {
	switch element.Name.Local {
	case "starttls":
		return FrameStartTLS
	case "auth":
		return FrameSASLAuth
	case "bind":
		return FrameResourceBind
	case "iq":
		if element.Child("bind") != nil {
			return FrameResourceBind
		}
		return FrameStanza
	case "message", "presence":
		return FrameStanza
	default:
		return FrameStanza
	}
}

// DrainFrames reads all currently complete frames. It is primarily useful in
// tests and simple batch integrations. Production network loops should call
// NextFrame directly and stop after StartTLS until UpgradeTLS succeeds.
func DrainFrames(r *FrameReader) ([]*Frame, error) {
	var frames []*Frame
	for {
		frame, err := r.NextFrame()
		if errors.Is(err, ErrNeedMore) {
			return frames, nil
		}
		if err != nil {
			return frames, err
		}
		frames = append(frames, frame)
	}
}

// ParseFrames parses all complete frames in data. It is a convenience wrapper
// around NewFrameReader and DrainFrames; production stream connections should
// retain a FrameReader or Guard across reads.
func ParseFrames(data []byte, limits ...Limits) ([]*Frame, error) {
	reader := NewFrameReader(limits...)
	if _, err := reader.Write(data); err != nil {
		return nil, err
	}
	return DrainFrames(reader)
}
