package netsec

import (
	"io"
)

// DecodedPart is a fully buffered multipart part returned by DecodeMultipart.
type DecodedPart struct {
	Header map[string][]string
	Body   []byte
}

// DecodeMultipart parses a complete multipart body from r using the boundary
// declared in contentType. It is a convenience wrapper around NewReader for
// upload-style payloads that fit comfortably in memory.
//
// The boundary parameter in contentType may carry optional double quotes;
// they are removed before delimiter matching. Each returned part carries its
// header block and exact body bytes, with the boundary transport padding
// validated according to RFC 2046.
func DecodeMultipart(contentType string, r io.Reader) ([]DecodedPart, error) {
	boundary, err := Boundary(contentType)
	if err != nil {
		return nil, err
	}
	mr, err := NewReader(r, boundary)
	if err != nil {
		return nil, err
	}

	var parts []DecodedPart
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		parts = append(parts, DecodedPart{
			Header: part.Header,
			Body:   body,
		})
	}
}
