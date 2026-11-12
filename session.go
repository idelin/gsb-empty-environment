package netsec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// Session owns protocol selection for one client connection. All requests on
// the connection must use the protocol selected by its first byte.
type Session struct {
	n        *Normalizer
	r        *bufio.Reader
	protocol Protocol
}

// NewSession creates a connection-scoped request reader.
func (n *Normalizer) NewSession(r io.Reader) *Session {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &Session{n: n, r: br}
}

// Protocol reports the locked protocol, or ProtocolUnknown before the first
// request.
func (s *Session) Protocol() Protocol {
	return s.protocol
}

// ReadRequest reads exactly one request, normalizes its keys, and returns the
// bytes to send upstream. Errors are fatal to the connection.
func (s *Session) ReadRequest() (*Request, error) {
	first, err := s.r.Peek(1)
	if err != nil {
		if err == io.EOF && len(first) == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	binaryFirst := first[0] == binaryRequestMagic

	switch s.protocol {
	case ProtocolUnknown:
		if binaryFirst {
			s.protocol = ProtocolBinary
		} else {
			s.protocol = ProtocolText
		}
	case ProtocolText:
		if binaryFirst {
			return nil, fmt.Errorf("%w: text connection received binary magic 0x80",
				ErrProtocolSwitch)
		}
	case ProtocolBinary:
		if !binaryFirst {
			return nil, fmt.Errorf("%w: binary connection received text command", ErrProtocolSwitch)
		}
	}

	if s.protocol == ProtocolBinary {
		if !s.n.cfg.AllowBinary {
			// Consume a well-framed probe so a following text byte is still
			// diagnosable as a switch. The connection is fatal either way, so
			// malformed framing can be collapsed into the same rejection.
			_ = s.consumeBinaryFrame()
			return nil, ErrBinaryUnsupported
		}
		return s.readBinaryRequest()
	}
	return s.readTextRequest()
}

// Normalize detects and normalizes one complete request in raw. No protocol
// state is retained; use NewSession for connection handling.
func (n *Normalizer) Normalize(raw []byte) (*Request, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty request", ErrMalformedRequest)
	}
	if raw[0] == binaryRequestMagic {
		if !n.cfg.AllowBinary {
			return nil, ErrBinaryUnsupported
		}
		return n.NormalizeBinary(raw)
	}
	return n.NormalizeText(raw)
}

// NormalizeText parses exactly one text request from raw.
func (n *Normalizer) NormalizeText(raw []byte) (*Request, error) {
	if len(raw) != 0 && raw[0] == binaryRequestMagic {
		return nil, fmt.Errorf("%w: text helper received binary magic 0x80", ErrProtocolSwitch)
	}
	br := bufio.NewReader(bytes.NewReader(raw))
	s := &Session{n: n, r: br, protocol: ProtocolText}
	req, err := s.readTextRequest()
	if err != nil {
		return nil, err
	}
	if _, err := br.Peek(1); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, ErrTrailingData
	}
	return req, nil
}

// NormalizeBinary parses exactly one binary request from raw. It is explicit
// about the protocol, so it does not consult Config.AllowBinary.
func (n *Normalizer) NormalizeBinary(raw []byte) (*Request, error) {
	br := bufio.NewReader(bytes.NewReader(raw))
	s := &Session{n: n, r: br, protocol: ProtocolBinary}
	req, err := s.readBinaryRequest()
	if err != nil {
		return nil, err
	}
	if _, err := br.Peek(1); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, ErrTrailingData
	}
	return req, nil
}
