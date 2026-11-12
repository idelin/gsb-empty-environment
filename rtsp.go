package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

const (
	StatusOK                  = 200
	StatusBadRequest          = 400
	StatusUnauthorized        = 401
	StatusMethodNotAllowed    = 405
	StatusSessionNotFound     = 454
	StatusMethodNotValidState = 455
	StatusInternalError       = 500
	StatusUnavailable         = 503
)

var statusReasons = map[int]string{
	StatusOK:                  "OK",
	StatusBadRequest:          "Bad Request",
	StatusUnauthorized:        "Unauthorized",
	StatusMethodNotAllowed:    "Method Not Allowed",
	StatusSessionNotFound:     "Session Not Found",
	StatusMethodNotValidState: "Method Not Valid in This State",
	StatusInternalError:       "Internal Server Error",
	StatusUnavailable:         "Service Unavailable",
}

// Request is an authenticated RTSP request presented to a Manager.
type Request struct {
	Method     string
	URI        string
	Principal  string
	RemoteAddr string
	CSeq       uint32
	Session    string
	Transport  string
	Headers    http.Header
}

// Response is the result of an RTSP state transition.
type Response struct {
	StatusCode     int
	Reason         string
	CSeq           uint32
	SessionID      string
	SessionTimeout int
	Target         *MediaTarget
	Headers        http.Header
}

// OK reports whether the response has a success status.
func (r Response) OK() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}

// WriteRTSP writes the response in RTSP/1.0 wire format.
func (r Response) WriteRTSP(w io.Writer) error {
	if w == nil {
		return errors.New("nil response writer")
	}
	headers := r.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	if headers.Get("CSeq") == "" {
		headers.Set("CSeq", strconv.FormatUint(uint64(r.CSeq), 10))
	}
	reason := r.Reason
	if reason == "" {
		reason = statusReasons[r.StatusCode]
	}
	if reason == "" {
		reason = "Unknown"
	}
	if _, err := fmt.Fprintf(w, "RTSP/1.0 %d %s\r\n", r.StatusCode, reason); err != nil {
		return err
	}
	if err := headers.Write(w); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

// ProtocolError carries an RTSP status through media-send helpers.
type ProtocolError struct {
	StatusCode int
	Reason     string
}

func (e *ProtocolError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("rtsp %d: %s", e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("rtsp %d: %s", e.StatusCode, statusReasons[e.StatusCode])
}

var (
	// ErrSessionNotFound is also returned for a session used by a different principal.
	ErrSessionNotFound = &ProtocolError{StatusCode: StatusSessionNotFound, Reason: statusReasons[StatusSessionNotFound]}
	// ErrNotPlaying prevents media before a successful PLAY.
	ErrNotPlaying = &ProtocolError{StatusCode: StatusMethodNotValidState, Reason: statusReasons[StatusMethodNotValidState]}
	// ErrNoUDPTransport is returned when media is written for an interleaved TCP session.
	ErrNoUDPTransport = &ProtocolError{StatusCode: StatusMethodNotValidState, Reason: "Session does not use UDP transport"}
	// ErrInvalidMediaWriter is returned for a nil packet connection.
	ErrInvalidMediaWriter = errors.New("nil media packet connection")
)

func newResponse(code int, cseq uint32) Response {
	headers := make(http.Header)
	headers.Set("CSeq", strconv.FormatUint(uint64(cseq), 10))
	return Response{
		StatusCode: code,
		Reason:     statusReasons[code],
		CSeq:       cseq,
		Headers:    headers,
	}
}

func badRequest(cseq uint32, detail string) Response {
	r := newResponse(StatusBadRequest, cseq)
	if detail != "" {
		r.Headers.Set("X-RTSP-Error", detail)
	}
	return r
}

func sessionNotFound(cseq uint32) Response {
	return newResponse(StatusSessionNotFound, cseq)
}

// ParseRequest parses one RTSP/1.0 request from its wire representation.
// Authentication identity and the peer address are filled in by the caller.
func ParseRequest(raw string) (*Request, error) {
	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(raw)))
	requestLine, err := reader.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("read request line: %w", err)
	}
	fields := strings.Fields(requestLine)
	if len(fields) != 3 {
		return nil, errors.New("malformed RTSP request line")
	}
	if !strings.EqualFold(fields[2], "RTSP/1.0") {
		return nil, fmt.Errorf("unsupported RTSP version %q", fields[2])
	}

	mimeHeader, err := reader.ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("read RTSP headers: %w", err)
	}
	headers := http.Header(mimeHeader)

	cseqValue, err := singleMIMEValue(mimeHeader, "CSeq")
	if err != nil {
		return nil, err
	}
	if cseqValue == "" {
		return nil, errors.New("missing CSeq header")
	}
	cseq, err := strconv.ParseUint(strings.TrimSpace(cseqValue), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid CSeq %q", cseqValue)
	}

	sessionValue, err := singleMIMEValue(mimeHeader, "Session")
	if err != nil {
		return nil, err
	}
	sessionID := sessionValue
	if sessionValue != "" {
		sessionID = strings.TrimSpace(strings.SplitN(sessionValue, ";", 2)[0])
		if sessionID == "" {
			return nil, errors.New("empty Session header")
		}
	}

	transport, err := singleMIMEValue(mimeHeader, "Transport")
	if err != nil {
		return nil, err
	}

	return &Request{
		Method:    strings.ToUpper(fields[0]),
		URI:       fields[1],
		CSeq:      uint32(cseq),
		Session:   sessionID,
		Transport: strings.TrimSpace(transport),
		Headers:   headers,
	}, nil
}

func singleMIMEValue(header textproto.MIMEHeader, key string) (string, error) {
	var values []string
	for name, candidates := range header {
		if strings.EqualFold(name, key) {
			values = append(values, candidates...)
		}
	}
	if len(values) > 1 {
		return "", fmt.Errorf("duplicate %s header", key)
	}
	if len(values) == 0 {
		return "", nil
	}
	return strings.TrimSpace(values[0]), nil
}
