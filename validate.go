package netsec

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var allowedRequestPseudo = map[string]bool{
	":method":    true,
	":scheme":    true,
	":path":      true,
	":authority": true,
}

// connectionSpecific lists hop-by-hop headers that RFC 7540 section 8.1.2.2
// forbids in HTTP/2 and that must never be emitted to an HTTP/1 peer.
var connectionSpecific = map[string]bool{
	"connection":        true,
	"keep-alive":        true,
	"proxy-connection":  true,
	"transfer-encoding": true,
	"upgrade":           true,
	"trailer":           true,
}

// trailerForbidden lists fields that are meaningless or dangerous in a
// trailer section and must be rejected rather than downgraded.
var trailerForbidden = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
	"keep-alive":        true,
	"proxy-connection":  true,
	"upgrade":           true,
	"trailer":           true,
	"te":                true,
}

func (c *Converter) validateHeaders(fields []hpackHeaderField) error {
	sawRegular := false
	var hostValue string
	hostCount := 0

	for _, hf := range fields {
		name := hf.Name
		if name == "" {
			return errors.New("netsec: empty header field name")
		}
		if strings.ContainsAny(name, "\x00\r\n") || strings.ContainsAny(hf.Value, "\x00\r\n") {
			return errors.New("netsec: NUL/CR/LF in header field")
		}
		isPseudo := name[0] == ':'
		if isPseudo {
			if sawRegular {
				return fmt.Errorf("%w: %s after regular header", ErrPseudoOrder, name)
			}
			if !allowedRequestPseudo[name] {
				return fmt.Errorf("%w: %s", ErrUnknownPseudo, name)
			}
			if _, dup := c.pseudo[name]; dup {
				return fmt.Errorf("%w: %s", ErrDuplicatePseudo, name)
			}
			c.pseudo[name] = hf.Value
			continue
		}

		sawRegular = true
		lower := strings.ToLower(name)
		if lower != name {
			// RFC 7540 section 8.1.2: field names must be lowercase.
			return fmt.Errorf("netsec: uppercase header field name %q", name)
		}
		if !validToken(lower) {
			return fmt.Errorf("netsec: invalid header field name %q", name)
		}
		switch lower {
		case "host":
			hostCount++
			if hostCount > 1 {
				return errors.New("netsec: duplicate Host header")
			}
			hostValue = hf.Value
		case "content-length":
			length, err := parseContentLength(hf.Value)
			if err != nil {
				return err
			}
			if c.hasLength && length != c.declaredLen {
				return fmt.Errorf("%w: conflicting Content-Length values", ErrLengthMismatch)
			}
			c.declaredLen = length
			c.hasLength = true
		case "te":
			// RFC 7540 8.1.2.2: TE is only allowed with the value "trailers".
			if hf.Value != "trailers" {
				return fmt.Errorf("%w: te: %q", ErrConnectionField, hf.Value)
			}
		default:
			if connectionSpecific[lower] {
				return fmt.Errorf("%w: %s", ErrConnectionField, lower)
			}
		}
		c.headers = append(c.headers, h2Field{Name: lower, Value: hf.Value})
	}

	for _, required := range []string{":method", ":scheme", ":path"} {
		if _, ok := c.pseudo[required]; !ok {
			return fmt.Errorf("%w: %s", ErrMissingPseudo, required)
		}
	}
	if !validToken(c.pseudo[":method"]) {
		return fmt.Errorf("netsec: invalid :method %q", c.pseudo[":method"])
	}
	if c.pseudo[":scheme"] != "http" && c.pseudo[":scheme"] != "https" {
		return fmt.Errorf("netsec: invalid :scheme %q", c.pseudo[":scheme"])
	}
	if !validPath(c.pseudo[":path"]) {
		return fmt.Errorf("netsec: invalid :path %q", c.pseudo[":path"])
	}

	authority := c.pseudo[":authority"]
	switch {
	case authority != "" && hostCount > 0 && authority != hostValue:
		return fmt.Errorf("%w: :authority %q vs Host %q", ErrAuthorityMismatch, authority, hostValue)
	case authority == "" && hostCount > 0:
		c.pseudo[":authority"] = hostValue
	case authority != "" && hostCount == 0:
		// Host is synthesized from :authority when building the request.
	}
	if c.pseudo[":authority"] == "" {
		return fmt.Errorf("%w: :authority", ErrMissingPseudo)
	}
	return nil
}

func (c *Converter) validateTrailer(fields []hpackHeaderField) error {
	if c.trailer != nil {
		return fmt.Errorf("%w: multiple trailer sections", ErrTrailer)
	}
	trailer := make(http.Header)
	for _, hf := range fields {
		name := hf.Name
		if name == "" || name[0] == ':' {
			return fmt.Errorf("%w: pseudo-header %q in trailers", ErrTrailer, name)
		}
		if strings.ContainsAny(name, "\x00\r\n") || strings.ContainsAny(hf.Value, "\x00\r\n") {
			return fmt.Errorf("%w: NUL/CR/LF in trailer field", ErrTrailer)
		}
		lower := strings.ToLower(name)
		if lower != name || !validToken(lower) {
			return fmt.Errorf("%w: invalid trailer field name %q", ErrTrailer, name)
		}
		if trailerForbidden[lower] {
			return fmt.Errorf("%w: %s in trailers", ErrTrailer, lower)
		}
		trailer.Add(lower, hf.Value)
	}
	c.trailer = trailer
	return nil
}

func parseContentLength(value string) (int64, error) {
	// net/http allows leading/trailing OWS; reject anything else.
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t") {
		return 0, fmt.Errorf("%w: non-numeric Content-Length %q", ErrLengthMismatch, value)
	}
	length, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || length < 0 {
		return 0, fmt.Errorf("%w: invalid Content-Length %q", ErrLengthMismatch, value)
	}
	return length, nil
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b <= 0x20 || b >= 0x7f {
			return false
		}
		switch b {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"',
			'/', '[', ']', '?', '=', '{', '}':
			return false
		}
	}
	return true
}

func validPath(p string) bool {
	if p == "" || p[0] != '/' {
		return false
	}
	if strings.ContainsAny(p, "\x00\r\n ") {
		return false
	}
	return true
}

func (c *Converter) buildRequest() (*http.Request, error) {
	target := c.pseudo[":scheme"] + "://" + c.pseudo[":authority"] + c.pseudo[":path"]
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("netsec: cannot build request target: %w", err)
	}
	bodyBytes := c.body.Bytes()
	req := &http.Request{
		Method: c.pseudo[":method"],
		URL:    parsed,
		Host:   c.pseudo[":authority"],
		Header: make(http.Header),
	}

	hasTrailer := len(c.trailer) > 0
	for _, hf := range c.headers {
		switch hf.Name {
		case "host", "te", "content-length", "trailer":
			continue
		}
		req.Header.Add(hf.Name, hf.Value)
	}
	if hasTrailer {
		names := make([]string, 0, len(c.trailer))
		for name := range c.trailer {
			names = append(names, name)
		}
		req.Header.Set("Trailer", strings.Join(names, ", "))
	}

	switch {
	case hasTrailer:
		// Chunked framing is the only HTTP/1 mechanism that carries trailers.
		// net/http applies the chunked wrapper itself based on TransferEncoding.
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		req.ContentLength = -1
		req.TransferEncoding = []string{"chunked"}
		req.Trailer = c.trailer.Clone()
	case len(bodyBytes) > 0 || c.hasLength:
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		req.ContentLength = int64(len(bodyBytes))
	default:
		req.Body = http.NoBody
		req.ContentLength = 0
	}
	return req, nil
}
