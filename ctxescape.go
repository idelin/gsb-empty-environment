// Package ctxescape provides a context-aware HTML escaping writer for Go
// rendering services that interpolate untrusted text into a template stream.
//
// A Writer incrementally parses the trusted template bytes it receives and
// keeps track of the HTML parse context across Write boundaries: template
// fragments may be split at any byte. When untrusted data is written with
// WriteText, the data is escaped according to the current context (HTML text,
// quoted or unquoted attribute, URL attribute, JavaScript string, ...).
//
// Security is fail-closed: if the current context cannot be safely modeled
// (an unterminated tag at the insertion point, an HTML comment, a CDATA
// section, a script statement position, a style block, ...), WriteText
// returns an error and emits nothing instead of guessing an escaping rule.
package ctxescape

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Parser states. The state machine is a small, streaming re-implementation of
// the relevant subset of the HTML5 tokenizer used by html/template.
type state int

const (
	stateText state = iota
	stateTagName
	stateTag
	stateAttrName
	stateAfterName
	stateBeforeValue
	stateAttrValueQ // quoted text attribute
	stateAttrValueU // unquoted text attribute
	stateURLValueQ  // quoted URL attribute
	stateURLValueU  // unquoted URL attribute
	stateJSAttrQ    // quoted event-handler attribute
	stateJSAttrU    // unquoted event-handler attribute
	stateStyleQ     // quoted style attribute (modeled, interpolation refused)
	stateStyleU     // unquoted style attribute (modeled, interpolation refused)
	stateHTMLComment
	stateCDATA
	stateDecl // <!DOCTYPE ...> and other declarations
	stateRawText
	stateJSCode     // inside <script>, statement position
	stateJSDqStr    // inside <script>, "..." string
	stateJSSqStr    // inside <script>, '...' string
	stateJSTmpl     // inside <script>, `...` template literal
	stateJSRegexp   // inside <script>, /.../ regexp literal
	stateJSBlockCmt // inside <script>, /* ... */ comment
	stateJSLineCmt  // inside <script>, // ... line comment
)

func (s state) String() string {
	names := [...]string{
		stateText:        "html text",
		stateTagName:     "tag name",
		stateTag:         "inside tag",
		stateAttrName:    "attribute name",
		stateAfterName:   "after attribute name",
		stateBeforeValue: "before attribute value",
		stateAttrValueQ:  "quoted attribute value",
		stateAttrValueU:  "unquoted attribute value",
		stateURLValueQ:   "quoted URL attribute value",
		stateURLValueU:   "unquoted URL attribute value",
		stateJSAttrQ:     "quoted event handler attribute",
		stateJSAttrU:     "unquoted event handler attribute",
		stateStyleQ:      "quoted style attribute",
		stateStyleU:      "unquoted style attribute",
		stateHTMLComment: "HTML comment",
		stateCDATA:       "CDATA section",
		stateDecl:        "markup declaration",
		stateRawText:     "raw text element body",
		stateJSCode:      "JavaScript program text",
		stateJSDqStr:     "JavaScript double-quoted string",
		stateJSSqStr:     "JavaScript single-quoted string",
		stateJSTmpl:      "JavaScript template literal",
		stateJSRegexp:    "JavaScript regexp literal",
		stateJSBlockCmt:  "JavaScript block comment",
		stateJSLineCmt:   "JavaScript line comment",
	}
	if int(s) < len(names) && names[s] != "" {
		return names[s]
	}
	return "unknown"
}

// elementKind identifies the raw-text element currently open.
type elementKind int

const (
	elemGeneric elementKind = iota
	elemScript
	elemStyle
	elemRCDATA // textarea or title
)

// attributeKind classifies the attribute currently being parsed.
type attributeKind int

const (
	attrPlain attributeKind = iota
	attrURL
	attrStyle
	attrJS
)

var (
	// ErrAmbiguousContext is returned when untrusted text cannot be safely
	// interpolated because the current parse state cannot be modeled or the
	// last template chunk ends inside an incomplete markup construct.
	ErrAmbiguousContext = errors.New("ctxescape: ambiguous or unsupported interpolation context")
	// ErrUnsafeURL is returned when a URL attribute receives a value whose
	// scheme is not an allow-listed navigation scheme.
	ErrUnsafeURL = errors.New("ctxescape: unsafe URL scheme")
	// ErrIncompleteStream is returned by Close when the stream ends inside an
	// unfinished markup construct (dangling '<', unterminated comment, ...).
	ErrIncompleteStream = errors.New("ctxescape: stream ended inside incomplete markup")
)

// Writer is an io.WriteCloser. Trusted template bytes flow through Write;
// untrusted, context-escaped text flows through WriteText.
//
// After any error the Writer is poisoned: further calls return the same
// error, so a failure can never be partially flushed downstream.
type Writer struct {
	out io.Writer

	st   state
	elem elementKind
	attr attributeKind
	// elemName is the lowercase name of the raw-text element currently open.
	elemName string
	// closingTag marks that the tag being collected started with "</".
	closingTag bool

	// pending holds template bytes that have not been consumed because they
	// might be the prefix of a longer multi-byte construct (for example a
	// dangling '<' at the end of a chunk).
	pending []byte

	// nameBuf accumulates the current tag or attribute name byte by byte, so
	// name capture is independent of how the input is chunked or flushed.
	nameBuf []byte

	// JS parser bookkeeping.
	jsDivExpected bool // heuristic: next '/' starts a regexp rather than division
	jsBraceDepth  []int

	// quote is the delimiter of the attribute value currently being parsed.
	quote byte

	err error
}

// NewWriter returns a Writer that renders into out.
func NewWriter(out io.Writer) *Writer {
	return &Writer{out: out, st: stateText}
}

// State reports the current parser state. It is intended for diagnostics and
// tests; the state names are not a stable API.
func (w *Writer) State() string { return w.st.String() }

// Write appends trusted template bytes. The bytes are assumed to be authored
// by the program, not by an end user; they drive the parse state machine and
// are forwarded verbatim. A chunk may end at any byte, including in the
// middle of a tag, comment or multi-byte rune.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	w.pending = append(w.pending, p...)
	if err := w.advance(); err != nil {
		w.fail(err)
		return 0, err
	}
	return len(p), nil
}

// WriteString writes trusted template text. It is a convenience wrapper
// around Write.
func (w *Writer) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// WriteText appends untrusted text, escaping it for the current parse
// context. The parse state is not changed by the escaped output.
//
// It returns ErrAmbiguousContext (without writing anything) when the current
// context cannot be safely handled, and ErrUnsafeURL when a URL attribute
// value uses a disallowed scheme.
func (w *Writer) WriteText(s string) error {
	if w.err != nil {
		return w.err
	}
	if len(w.pending) != 0 {
		return w.fail(fmt.Errorf("%w: template chunk ends inside %s",
			ErrAmbiguousContext, w.st))
	}
	if w.st == stateBeforeValue {
		w.quote = 0
		w.st = w.unquotedValueState()
	}
	escaped, err := w.escape(s)
	if err != nil {
		return w.fail(err)
	}
	if _, err := w.out.Write([]byte(escaped)); err != nil {
		return w.fail(err)
	}
	return nil
}

// Close flushes any remaining buffered template bytes and verifies that the
// stream ends in a well-defined context.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if len(w.pending) != 0 || w.st != stateText {
		return w.fail(fmt.Errorf("%w: %q left in state %s",
			ErrIncompleteStream, w.pending, w.st))
	}
	return nil
}

func (w *Writer) fail(err error) error {
	w.err = err
	return err
}

// advance runs the state machine over pending, emitting every byte that is
// known not to be the prefix of an unrecognized construct. Bytes that may
// still be extended by a later chunk remain buffered in pending.
func (w *Writer) advance() error {
	for len(w.pending) > 0 {
		prevState := w.st
		n, err := w.step()
		if err != nil {
			return err
		}
		if n == 0 {
			if w.st != prevState {
				// The step changed context without consuming bytes (for
				// example entering an unquoted value on the first value
				// byte); let the new state process the same buffer.
				continue
			}
			// The whole buffer is a possible prefix of a longer construct.
			return nil
		}
		if n > len(w.pending) {
			return fmt.Errorf("ctxescape: internal error, step consumed %d of %d bytes",
				n, len(w.pending))
		}
		if _, err := w.out.Write(w.pending[:n]); err != nil {
			return err
		}
		w.pending = w.pending[n:]
	}
	return nil
}

// step consumes zero or more bytes from pending, transitioning the state
// machine. A return of n == 0 means the current buffer is an incomplete
// prefix that must wait for more bytes.
func (w *Writer) step() (int, error) {
	switch w.st {
	case stateText:
		return w.stepText()
	case stateTagName:
		return w.stepTagName()
	case stateTag:
		return w.stepTag()
	case stateAttrName:
		return w.stepAttrName()
	case stateAfterName:
		return w.stepAfterName()
	case stateBeforeValue:
		return w.stepBeforeValue()
	case stateAttrValueQ, stateURLValueQ, stateJSAttrQ, stateStyleQ:
		return w.stepQuotedValue()
	case stateAttrValueU, stateURLValueU, stateJSAttrU, stateStyleU:
		return w.stepUnquotedValue()
	case stateHTMLComment:
		return w.stepUntil("-->")
	case stateCDATA:
		return w.stepUntil("]]>")
	case stateDecl:
		return w.stepDecl()
	case stateRawText:
		return w.stepRawText()
	case stateJSCode:
		return w.stepJSCode()
	case stateJSDqStr, stateJSSqStr:
		return w.stepJSString()
	case stateJSTmpl:
		return w.stepJSTmpl()
	case stateJSRegexp:
		return w.stepJSRegexp()
	case stateJSBlockCmt:
		return w.stepJSBlockCmt()
	case stateJSLineCmt:
		return w.stepJSLineCmt()
	default:
		return 0, fmt.Errorf("%w: unmodeled state %d", ErrAmbiguousContext, w.st)
	}
}

func (w *Writer) stepText() (int, error) {
	i := bytes.IndexByte(w.pending, '<')
	if i < 0 {
		return len(w.pending), nil
	}
	if i == len(w.pending)-1 {
		// Dangling '<': wait for the next chunk.
		return 0, nil
	}
	switch w.pending[i+1] {
	case '!':
		if bytes.HasPrefix(w.pending[i:], []byte("<!--")) {
			if len(w.pending) < i+4 {
				return 0, nil
			}
			w.st = stateHTMLComment
			return i + 4, nil
		}
		if bytes.HasPrefix(w.pending[i:], []byte("<![CDATA[")) {
			if len(w.pending) < i+9 {
				return 0, nil
			}
			w.st = stateCDATA
			return i + 9, nil
		}
		// Unknown or partial "<!..." declaration.
		if hasCaseInsensitivePrefix(w.pending[i:], "<!doctype") && len(w.pending) < i+9 {
			return 0, nil
		}
		w.st = stateDecl
		return i + 2, nil
	case '/':
		if i+2 == len(w.pending) {
			return 0, nil
		}
		if !isASCIIAlpha(w.pending[i+2]) {
			// Malformed closing tag: treat the '<' as text and continue.
			return i + 1, nil
		}
		w.closingTag = true
		w.nameBuf = w.nameBuf[:0]
		w.st = stateTagName
		// Consume through the first name byte so it lands in nameBuf.
		w.nameBuf = append(w.nameBuf, w.pending[i+2])
		return i + 3, nil
	case '?':
		// Processing instructions are not valid HTML; consume the '<' as text.
		return i + 1, nil
	default:
		if !isASCIIAlpha(w.pending[i+1]) {
			// '<' not followed by a tag name: literal text.
			return i + 1, nil
		}
		w.closingTag = false
		w.nameBuf = w.nameBuf[:0]
		w.nameBuf = append(w.nameBuf, w.pending[i+1])
		w.st = stateTagName
		return i + 2, nil
	}
}

func (w *Writer) stepTagName() (int, error) {
	i := 0
	for i < len(w.pending) {
		c := w.pending[i]
		switch {
		case isHTMLSpace(c):
			w.commitTagName()
			w.st = stateTag
			return i + 1, nil
		case c == '>':
			w.commitTagName()
			w.enterElementBody()
			return i + 1, nil
		case c == '/':
			if i == len(w.pending)-1 {
				return 0, nil
			}
			if w.pending[i+1] == '>' {
				// Void-element-style self close; no body context to enter.
				w.st = stateText
				w.elem = elemGeneric
				return i + 2, nil
			}
			i++
			continue
		default:
			if !isTagNameByte(c) {
				i++
				continue
			}
			w.nameBuf = append(w.nameBuf, c)
		}
		i++
	}
	// Name may continue in the next chunk.
	return i, nil
}

func (w *Writer) commitTagName() {
	name := strings.ToLower(string(w.nameBuf))
	w.elemName = name
	switch name {
	case "script":
		w.elem = elemScript
	case "style":
		w.elem = elemStyle
	case "textarea", "title":
		w.elem = elemRCDATA
	default:
		w.elem = elemGeneric
	}
}

// enterElementBody is called when '>' closes an opening tag.
func (w *Writer) enterElementBody() {
	if w.closingTag {
		w.st = stateText
		w.elem = elemGeneric
		w.elemName = ""
		w.attr = attrPlain
		w.closingTag = false
		return
	}
	switch w.elem {
	case elemScript:
		w.st = stateJSCode
		w.jsDivExpected = true
	case elemStyle, elemRCDATA:
		w.st = stateRawText
	default:
		w.st = stateText
	}
}
