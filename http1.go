package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// WriteHTTP1 serializes the validated request as a raw HTTP/1.1 message in the
// exact wire form that should be sent to the downstream HTTP/1 peer:
// request line, Host, regular headers, framing headers, body and optional
// trailer section. It must only be called after Ready returns true.
//
// The output is independent of net/http's own transport so proxies (and tests)
// can assert the exact bytes crossing the version boundary.
func (c *Converter) WriteHTTP1(w io.Writer) error {
	req := c.Request()
	if req == nil {
		return errors.New("netsec: request is not ready")
	}
	var out bytes.Buffer

	target := req.URL.RequestURI()
	if target == "" {
		target = "/"
	}
	fmt.Fprintf(&out, "%s %s HTTP/1.1\r\n", req.Method, target)
	fmt.Fprintf(&out, "Host: %s\r\n", req.Host)

	// Emit regular headers in the original HTTP/2 wire order, preserving
	// duplicates and using the canonical HTTP/1 capitalization.
	for _, hf := range c.headers {
		switch hf.Name {
		case "host", "te", "content-length", "trailer":
			continue
		}
		fmt.Fprintf(&out, "%s: %s\r\n", http.CanonicalHeaderKey(hf.Name), hf.Value)
	}

	hasTrailer := len(c.trailer) > 0
	body := c.body.Bytes()
	if hasTrailer {
		names := make([]string, 0, len(c.trailer))
		for name := range c.trailer {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(&out, "Trailer: %s\r\n", strings.Join(names, ", "))
		fmt.Fprint(&out, "Transfer-Encoding: chunked\r\n\r\n")
		writeChunkedBody(&out, body)
		for _, name := range names {
			for _, v := range c.trailer[name] {
				fmt.Fprintf(&out, "%s: %s\r\n", http.CanonicalHeaderKey(name), v)
			}
		}
		fmt.Fprint(&out, "\r\n")
	} else {
		fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n", len(body))
		out.Write(body)
	}

	_, err := w.Write(out.Bytes())
	return err
}

func writeChunkedBody(buf *bytes.Buffer, body []byte) {
	for len(body) > 0 {
		chunk := body
		if len(chunk) > 8192 {
			chunk = chunk[:8192]
		}
		fmt.Fprintf(buf, "%x\r\n", len(chunk))
		buf.Write(chunk)
		buf.WriteString("\r\n")
		body = body[len(chunk):]
	}
	buf.WriteString("0\r\n")
}
