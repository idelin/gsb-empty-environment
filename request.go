package h2h1

import (
	"errors"
	"strconv"
)

// Request is one syntactically and semantically validated HTTP/2 request.
// Headers contains the complete decoded header block, including pseudo headers.
type Request struct {
	StreamID uint32

	Method    string
	Scheme    string
	Path      string
	Authority string
	Protocol  string

	Headers  []HeaderField
	Trailers []HeaderField
	Body     []byte

	ContentLength    int64
	HasContentLength bool
}

// Validate re-applies all request semantics and the DATA/Content-Length
// equality invariant.
func (r *Request) Validate() error {
	if r == nil {
		return errors.New("h2h1: nil request")
	}
	if len(r.Headers) == 0 {
		r.Headers = []HeaderField{
			{Name: ":method", Value: r.Method},
			{Name: ":scheme", Value: r.Scheme},
			{Name: ":path", Value: r.Path},
		}
		if r.Authority != "" {
			r.Headers = append(r.Headers, HeaderField{Name: ":authority", Value: r.Authority})
		}
		if r.Protocol != "" {
			r.Headers = append(r.Headers, HeaderField{Name: ":protocol", Value: r.Protocol})
		}
		if r.HasContentLength {
			r.Headers = append(r.Headers, HeaderField{
				Name:  "content-length",
				Value: strconv.FormatInt(r.ContentLength, 10),
			})
		}
	}
	meta, err := ValidateRequestHeaders(r.Headers)
	if err != nil {
		return err
	}
	if err := ValidateContentLength(meta.ContentLength, meta.HasContentLength, r.Body); err != nil {
		return err
	}
	if err := ValidateTrailers(r.Trailers); err != nil {
		return err
	}
	r.applyMeta(meta)
	return nil
}

// RegularHeaders returns a copy of non-pseudo request fields.
func (r *Request) RegularHeaders() []HeaderField {
	if r == nil {
		return nil
	}
	out := make([]HeaderField, 0, len(r.Headers))
	for _, field := range r.Headers {
		if field.Name == "" || field.Name[0] != ':' {
			out = append(out, field)
		}
	}
	return out
}

func (r *Request) applyMeta(meta *RequestMeta) {
	r.Method = meta.Method
	r.Scheme = meta.Scheme
	r.Path = meta.Path
	r.Authority = meta.Authority
	r.Protocol = meta.Protocol
	r.Headers = meta.Headers
	r.ContentLength = meta.ContentLength
	r.HasContentLength = meta.HasContentLength
}
