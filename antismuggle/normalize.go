package antismuggle

import (
	"bytes"
	"strconv"
)

// NormalizedRequest is a canonical, re-framed representation safe to forward
// to heterogeneous backends. Ambiguous framing headers are gone: the body is
// always identity delimited by a single validated Content-Length.
type NormalizedRequest struct {
	Method  string
	Target  string
	Version string
	Fields  []Field
	Body    []byte
	// ChunkedPassthrough is true when the validated chunked frame is retained
	// verbatim; Body then contains the raw chunked bytes and Fields keep TE.
	ChunkedPassthrough bool
}

// Normalize fully reads and validates the body, then produces a canonical
// frame. On return the underlying Conn is positioned at the next request.
func Normalize(req *Request) (*NormalizedRequest, error) {
	body, err := ReadBodyAll(req)
	if err != nil {
		return nil, err
	}

	out := &NormalizedRequest{
		Method:  req.Method,
		Target:  req.Target,
		Version: req.Version,
	}

	if req.Transfer == BodyChunked && !req.cfg.DecodeChunkedToBody {
		out.Fields = append(out.Fields, req.Fields...)
		out.Body = req.rawChunked
		out.ChunkedPassthrough = true
		return out, nil
	}

	out.Body = body
	for _, f := range req.Fields {
		switch {
		case equalFoldASCII(f.Name, "transfer-encoding"),
			equalFoldASCII(f.Name, "content-length"),
			// Trailers live in the chunked trailer section, never as headers.
			equalFoldASCII(f.Name, "trailer"):
			continue
		default:
			out.Fields = append(out.Fields, f)
		}
	}
	out.Fields = append(out.Fields, Field{
		Name:  "Content-Length",
		Value: strconv.Itoa(len(body)),
	})
	return out, nil
}

// Bytes serializes the normalized request using exact CRLF line endings.
func (n *NormalizedRequest) Bytes() []byte {
	var buf bytes.Buffer
	buf.WriteString(n.Method)
	buf.WriteByte(' ')
	buf.WriteString(n.Target)
	buf.WriteByte(' ')
	buf.WriteString(n.Version)
	buf.WriteString("\r\n")
	for _, f := range n.Fields {
		buf.WriteString(f.Name)
		buf.WriteString(": ")
		buf.WriteString(f.Value)
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")
	buf.Write(n.Body)
	return buf.Bytes()
}

// FieldByName returns the first value for a case-insensitive field name.
func (n *NormalizedRequest) FieldByName(name string) (Field, bool) {
	for _, f := range n.Fields {
		if equalFoldASCII(f.Name, name) {
			return f, true
		}
	}
	return Field{}, false
}
