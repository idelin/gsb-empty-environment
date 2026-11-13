package netsec

import (
	"bytes"
	"fmt"
)

// AppendObject appends a complete indirect object to the physical end of data.
//
// The object must have the form "N G obj ... endobj".  Existing bytes are not
// changed, so appending an object after a signature leaves that signature's
// byte range ending at the previous revision unless the caller explicitly
// updates the range.  The cross-reference table is not rewritten: this helper
// models an appended update and lets the caller manage cross-reference
// maintenance separately.
func AppendObject(data, object []byte) ([]byte, error) {
	if err := checkIndirectObject(object); err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(data)+len(object)+2)
	out = append(out, data...)
	if len(out) > 0 && !isPDFWhitespace(out[len(out)-1]) {
		out = append(out, '\n')
	}
	out = append(out, object...)
	if len(object) > 0 && !isPDFWhitespace(object[len(object)-1]) {
		out = append(out, '\n')
	}
	return out, nil
}

// AppendIncrementalObject is an alias for AppendObject.
func AppendIncrementalObject(data, object []byte) ([]byte, error) {
	return AppendObject(data, object)
}

// AppendObjectAfterSignature appends an object at the physical end of the
// document, after the last byte of every existing signature byte range.
func AppendObjectAfterSignature(data, object []byte) ([]byte, error) {
	return AppendObject(data, object)
}

// AppendObjectBody constructs an indirect object from an object number, a
// generation number, and a body, then appends it to data.
func AppendObjectBody(data []byte, objectNumber, generation int, body []byte) ([]byte, error) {
	if objectNumber < 0 || generation < 0 {
		return nil, fmt.Errorf("pdf: object and generation numbers must be non-negative")
	}
	object := make([]byte, 0, len(body)+32)
	object = append(object, fmt.Sprintf("%d %d obj\n", objectNumber, generation)...)
	object = append(object, body...)
	if len(body) > 0 && !isPDFWhitespace(body[len(body)-1]) {
		object = append(object, '\n')
	}
	object = append(object, "endobj\n"...)
	return AppendObject(data, object)
}

func checkIndirectObject(object []byte) error {
	trimmed := bytes.TrimSpace(object)
	if len(trimmed) == 0 {
		return fmt.Errorf("pdf: empty indirect object")
	}

	lexer := newPDFLexer(trimmed)
	var tokens []token
	for len(tokens) < 3 {
		tok, err := lexer.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			break
		}
		tokens = append(tokens, tok)
	}

	if len(tokens) < 3 {
		return fmt.Errorf("pdf: object is not an indirect object")
	}
	if tokens[0].kind != tokNumber || tokens[1].kind != tokNumber ||
		tokens[2].kind != tokKeyword || tokens[2].text != "obj" {
		return fmt.Errorf("pdf: object must begin with an indirect object header")
	}
	if tokens[0].start != 0 {
		return fmt.Errorf("pdf: unexpected bytes before the indirect object header")
	}
	if !bytes.HasSuffix(trimmed, []byte("endobj")) {
		return fmt.Errorf("pdf: object must end with endobj")
	}
	return nil
}
