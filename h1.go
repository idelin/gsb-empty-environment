package h2h1

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// H1Request is the result of the strict backend HTTP/1.1 parser.
type H1Request struct {
	Method  string
	Target  string
	Version string

	Headers []HeaderField
	Body    []byte
	Host    string

	ContentLength    int64
	HasContentLength bool
}

func safeHost(value string) error {
	if value == "" {
		return nil
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= 0x20 || value[i] == 0x7f {
			return reject("host", "contains whitespace or a control byte")
		}
	}
	return nil
}

func safeRequestTarget(target string) error {
	if target == "" {
		return errors.New("h2h1: empty HTTP/1 request target")
	}
	for i := 0; i < len(target); i++ {
		c := target[i]
		if c <= 0x20 || c == 0x7f {
			return errors.New("h2h1: HTTP/1 request target contains whitespace or a control byte")
		}
	}
	return nil
}

func safeMethod(method string) error {
	if method == "" {
		return errors.New("h2h1: empty HTTP/1 method")
	}
	for i := 0; i < len(method); i++ {
		if !isHeaderTokenRune(method[i]) {
			return errors.New("h2h1: HTTP/1 method is not a token")
		}
	}
	return nil
}

// RenderH1Request converts a validated HTTP/2 request to one canonical
// HTTP/1.1 request. It rejects rather than sanitising any ambiguous input.
// Pseudo headers are removed, Host is emitted once, TE: trailers is dropped,
// and the body is framed by one generated Content-Length field.
func RenderH1Request(req *Request) ([]byte, error) {
	if req == nil {
		return nil, errors.New("h2h1: nil request")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := safeMethod(req.Method); err != nil {
		return nil, err
	}
	if err := safeRequestTarget(req.Path); err != nil {
		return nil, err
	}

	host := req.Authority
	if err := safeHost(host); err != nil {
		return nil, err
	}
	var hostFields []string
	for _, field := range req.Headers {
		if field.Name == "host" {
			if err := safeHost(field.Value); err != nil {
				return nil, err
			}
			hostFields = append(hostFields, field.Value)
		}
	}
	if len(hostFields) > 1 {
		return nil, reject("host", "multiple Host headers")
	}
	if len(hostFields) == 1 {
		if host == "" {
			host = hostFields[0]
		} else if !strings.EqualFold(host, hostFields[0]) {
			return nil, reject("host", "conflicting :authority and Host")
		}
	}

	var out bytes.Buffer
	out.Grow(len(req.Body) + 256 + len(req.Headers)*32)
	out.WriteString(req.Method)
	out.WriteByte(' ')
	out.WriteString(req.Path)
	out.WriteString(" HTTP/1.1\r\n")
	if host != "" {
		out.WriteString("Host: ")
		out.WriteString(host)
		out.WriteString("\r\n")
	}
	for _, field := range req.Headers {
		if field.Name == "" || field.Name[0] == ':' || field.Name == "host" ||
			field.Name == "content-length" || field.Name == "te" {
			continue
		}
		if isConnectionSpecificHeader(field.Name) {
			return nil, reject("connection-header", "cannot render %q", field.Name)
		}
		if !validHeaderName(field.Name) {
			return nil, reject("header-name", "invalid name %q", field.Name)
		}
		if err := validHeaderValue(field.Value); err != nil {
			return nil, err
		}
		out.WriteString(field.Name)
		out.WriteString(": ")
		out.WriteString(field.Value)
		out.WriteString("\r\n")
	}
	if req.HasContentLength || len(req.Body) > 0 {
		out.WriteString("Content-Length: ")
		out.WriteString(strconv.Itoa(len(req.Body)))
		out.WriteString("\r\n")
	}
	out.WriteString("\r\n")
	out.Write(req.Body)
	return out.Bytes(), nil
}

// DowngradeH2Request is an alias for RenderH1Request.
func DowngradeH2Request(req *Request) ([]byte, error) {
	return RenderH1Request(req)
}

// SafeH1Request is an alias for RenderH1Request.
func SafeH1Request(req *Request) ([]byte, error) {
	return RenderH1Request(req)
}

func validH1Token(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isHeaderTokenRune(name[i]) {
			return false
		}
	}
	return true
}

// ParseH1Request strictly parses exactly one backend HTTP/1.1 request. It
// rejects bare CR/LF, obs-fold, NUL, any Transfer-Encoding, suspicious
// connection headers, conflicting Content-Length, and trailing bytes.
func ParseH1Request(raw []byte) (*H1Request, error) {
	headEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	if headEnd < 0 {
		return nil, errors.New("h2h1: HTTP/1 header terminator is missing")
	}
	head := raw[:headEnd]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, errors.New("h2h1: HTTP/1 headers contain NUL")
	}
	body := raw[headEnd+4:]
	lines := bytes.Split(head, []byte("\r\n"))
	if len(lines) == 0 {
		return nil, errors.New("h2h1: HTTP/1 request is empty")
	}
	for _, line := range lines {
		if bytes.IndexByte(line, '\r') >= 0 || bytes.IndexByte(line, '\n') >= 0 {
			return nil, errors.New("h2h1: HTTP/1 headers contain a bare CR or LF")
		}
	}

	requestLine := string(lines[0])
	if strings.ContainsAny(requestLine, "\t") {
		return nil, errors.New("h2h1: HTTP/1 request line contains a tab")
	}
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, errors.New("h2h1: malformed HTTP/1 request line")
	}
	if !validH1Token(parts[0]) {
		return nil, errors.New("h2h1: malformed HTTP/1 method")
	}
	if err := safeRequestTarget(parts[1]); err != nil {
		return nil, err
	}
	if parts[2] != "HTTP/1.1" && parts[2] != "HTTP/1.0" {
		return nil, fmt.Errorf("h2h1: unsupported HTTP version %q", parts[2])
	}
	out := &H1Request{
		Method:  parts[0],
		Target:  parts[1],
		Version: parts[2],
	}

	var contentLength int64
	hasContentLength := false
	var host string
	hostCount := 0
	for _, rawLine := range lines[1:] {
		line := string(rawLine)
		if line == "" {
			return nil, errors.New("h2h1: empty HTTP/1 header line")
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, errors.New("h2h1: obsolete folded HTTP/1 header")
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("h2h1: malformed HTTP/1 header line %q", line)
		}
		name := line[:colon]
		if !validH1Token(name) {
			return nil, fmt.Errorf("h2h1: invalid HTTP/1 header name %q", name)
		}
		value := strings.Trim(line[colon+1:], " \t")
		if err := validHeaderValue(value); err != nil {
			return nil, err
		}
		lowerName := strings.ToLower(name)
		if isConnectionSpecificHeader(lowerName) {
			return nil, reject("connection-header", "HTTP/1 backend request contains %q", lowerName)
		}
		switch lowerName {
		case "host":
			hostCount++
			if hostCount > 1 {
				return nil, reject("host", "multiple Host headers")
			}
			if err := safeHost(value); err != nil {
				return nil, err
			}
			host = value
		case "te":
			if !strings.EqualFold(value, "trailers") {
				return nil, reject("te", "only TE: trailers is permitted, got %q", value)
			}
		case "content-length":
			n, err := canonicalContentLength(value)
			if err != nil {
				return nil, err
			}
			if hasContentLength && n != contentLength {
				return nil, reject("content-length", "conflicting HTTP/1 values %d and %d", contentLength, n)
			}
			contentLength = n
			hasContentLength = true
		}
		out.Headers = append(out.Headers, HeaderField{Name: lowerName, Value: value})
	}
	out.Host = host
	out.HasContentLength = hasContentLength
	out.ContentLength = contentLength
	if hasContentLength {
		if int64(len(body)) < contentLength {
			return nil, reject("content-length", "backend body truncated: declared %d, got %d", contentLength, len(body))
		}
		if int64(len(body)) > contentLength {
			return nil, reject("content-length", "backend has %d trailing bytes after declared body", int64(len(body))-contentLength)
		}
	} else if len(body) != 0 {
		return nil, reject("content-length", "HTTP/1 request has %d body bytes without Content-Length", len(body))
	}
	out.Body = append([]byte(nil), body...)
	return out, nil
}

// ParseSingleH1Request is an alias for ParseH1Request.
func ParseSingleH1Request(raw []byte) (*H1Request, error) {
	return ParseH1Request(raw)
}
