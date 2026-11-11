package csp

import (
	"bytes"
	"io"
)

var scriptClose = []byte("</script>")

// SanitizingWriter wraps an output stream and rejects any <script> element
// whose nonce or content hash was not approved for the current request. It
// works across arbitrary Write boundaries, which is essential for streaming
// template output where a single element may span several chunks.
type SanitizingWriter struct {
	dst    io.Writer
	policy *Policy

	state   sanitizeState
	scratch bytes.Buffer // raw bytes not yet released to dst
	name    bytes.Buffer // tag name currently being read
	openTag bytes.Buffer // full open tag of the buffered script element
	body    bytes.Buffer // script body accumulated for hashing
	closeN  int          // matched prefix length of "</script>"
	hasSrc  bool         // current element declared a src attribute

	err error
}

type sanitizeState int

const (
	// stateText scans ordinary markup, retaining a dangling '<'.
	stateText sanitizeState = iota
	// stateTagName reads the identifier right after a retained '<'.
	stateTagName
	// stateScriptOpen buffers "<script ..." up to the closing '>'.
	stateScriptOpen
	// stateScriptBody buffers script content looking for "</script>".
	stateScriptBody
	// stateScriptClose tracks a partial "</script>" match.
	stateScriptClose
)

// NewSanitizingWriter wraps dst with per-request policy enforcement.
func NewSanitizingWriter(dst io.Writer, policy *Policy) *SanitizingWriter {
	return &SanitizingWriter{dst: dst, policy: policy}
}

// Err returns the first enforcement or downstream write error.
func (w *SanitizingWriter) Err() error { return w.err }

// Write feeds one chunk of the streamed document. On success the returned
// count equals len(data), satisfying io.Writer contracts used by templates.
func (w *SanitizingWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	for _, b := range data {
		if err := w.feed(b); err != nil {
			w.err = err
			return 0, err
		}
	}
	return len(data), nil
}

// Close must be called once the complete document has been written. An
// unterminated script element is rejected rather than silently emitted.
func (w *SanitizingWriter) Close() error {
	if w.err != nil {
		return w.err
	}
	switch w.state {
	case stateScriptOpen, stateScriptBody, stateScriptClose:
		w.err = ErrUnauthorized
		return w.err
	}
	return w.flushScratch()
}

func (w *SanitizingWriter) feed(b byte) error {
	switch w.state {
	case stateText:
		if b == '<' {
			w.scratch.WriteByte(b)
			w.name.Reset()
			w.state = stateTagName
			return nil
		}
		w.scratch.WriteByte(b)
		return w.flushScratch()

	case stateTagName:
		if isNameByte(b) {
			w.scratch.WriteByte(b)
			w.name.WriteByte(b)
			return nil
		}
		if lowerString(w.name.Bytes()) == "script" {
			w.openTag.Reset()
			w.openTag.Write(w.scratch.Bytes())
			if b == '>' {
				w.scratch.WriteByte(b)
				w.openTag.WriteByte(b)
				return w.finishOpenTag()
			}
			w.state = stateScriptOpen
			w.scratch.WriteByte(b)
			w.openTag.WriteByte(b)
			return nil
		}
		// Non-script element: release everything and keep scanning text.
		w.scratch.WriteByte(b)
		w.state = stateText
		return w.flushScratch()

	case stateScriptOpen:
		w.scratch.WriteByte(b)
		w.openTag.WriteByte(b)
		if b == '>' {
			return w.finishOpenTag()
		}
		return nil

	case stateScriptBody:
		if b == '<' {
			w.scratch.WriteByte(b)
			w.closeN = 1
			w.state = stateScriptClose
			return nil
		}
		w.scratch.WriteByte(b)
		w.body.WriteByte(b)
		return nil

	case stateScriptClose:
		if b == scriptClose[w.closeN] {
			w.scratch.WriteByte(b)
			w.closeN++
			if w.closeN == len(scriptClose) {
				if err := w.validateScript(); err != nil {
					return err
				}
				if err := w.flushScratch(); err != nil {
					return err
				}
				w.resetElement()
				w.state = stateText
			}
			return nil
		}
		// The '<...' prefix was ordinary content; reprocess b in body state.
		w.state = stateScriptBody
		w.body.Write(scriptClose[:w.closeN])
		return w.feed(b)
	}
	return nil
}

func (w *SanitizingWriter) finishOpenTag() error {
	w.body.Reset()
	w.closeN = 0
	w.state = stateScriptBody

	nonce, hasSrc, hasNonce := parseScriptAttrs(w.openTag.Bytes())
	w.hasSrc = hasSrc
	if hasSrc {
		// External scripts carry no hashable body; a trusted nonce is required.
		if !hasNonce || nonce != w.policy.Nonce() || !w.policy.isApproved(nonce) {
			return ErrUnauthorized
		}
	}
	return nil
}

// validateScript authorizes the fully buffered element against the policy.
func (w *SanitizingWriter) validateScript() error {
	if w.hasSrc {
		// The nonce was already verified when the open tag was consumed.
		return nil
	}
	nonce, hasNonce := attrValue(w.openTag.Bytes(), "nonce")
	if hasNonce && nonce == w.policy.Nonce() && w.policy.isApproved(nonce) {
		return nil
	}
	if w.policy.isApproved(hashScript(w.body.String())) {
		return nil
	}
	return ErrUnauthorized
}

func (w *SanitizingWriter) resetElement() {
	w.openTag.Reset()
	w.body.Reset()
	w.closeN = 0
	w.hasSrc = false
}

func (w *SanitizingWriter) flushScratch() error {
	if w.scratch.Len() == 0 {
		return nil
	}
	if _, err := w.dst.Write(w.scratch.Bytes()); err != nil {
		w.err = err
		return err
	}
	w.scratch.Reset()
	return nil
}

func isNameByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func lowerString(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// parseScriptAttrs extracts the nonce value and reports a src attribute.
func parseScriptAttrs(markup []byte) (nonce string, hasSrc, hasNonce bool) {
	nonce, hasNonce = attrValue(markup, "nonce")
	_, hasSrc = attrValue(markup, "src")
	return nonce, hasSrc, hasNonce
}

// attrValue returns the quoted or unquoted value of the named attribute.
func attrValue(markup []byte, name string) (string, bool) {
	lower := bytes.ToLower(markup)
	target := []byte(name)
	for i := 0; i+len(name) <= len(lower); i++ {
		if lower[i] != target[0] || !bytes.Equal(lower[i:i+len(name)], target) {
			continue
		}
		if i > 0 && isNameByte(lower[i-1]) {
			continue
		}
		j := i + len(name)
		for j < len(lower) && lower[j] == ' ' {
			j++
		}
		if j >= len(lower) || lower[j] != '=' {
			continue
		}
		j++
		for j < len(lower) && lower[j] == ' ' {
			j++
		}
		if j >= len(markup) {
			return "", false
		}
		if markup[j] == '"' || markup[j] == '\'' {
			quote := markup[j]
			start := j + 1
			end := bytes.IndexByte(markup[start:], quote)
			if end < 0 {
				return "", false
			}
			return string(markup[start : start+end]), true
		}
		start := j
		for j < len(markup) && markup[j] != ' ' && markup[j] != '>' && markup[j] != '/' {
			j++
		}
		return string(markup[start:j]), true
	}
	return "", false
}
