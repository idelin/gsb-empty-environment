package taskpkg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	ErrNotUpgraded    = errors.New("upgrade session: response not fully sent")
	ErrSessionAborted = errors.New("upgrade session: aborted")
)

// FrameHandler receives frames admitted by the ticket. Rejected frames are
// reported through OnReject and never reach OnData or OnManagement.
type FrameHandler struct {
	OnData       func(payload []byte)
	OnManagement func(payload []byte)
	OnReject     func(frameType byte, err error)
}

// Admitter issues upgrade sessions for HTTP requests that have already
// passed ordinary HTTP authentication.
type Admitter struct {
	issuer *Issuer
	ttl    time.Duration
}

// NewAdmitter returns an Admitter that signs tickets with secret and gives
// them the provided TTL (zero disables expiry).
func NewAdmitter(secret []byte, ticketTTL time.Duration) *Admitter {
	return &Admitter{issuer: NewIssuer(secret), ttl: ticketTTL}
}

// Issuer exposes the underlying ticket issuer.
func (a *Admitter) Issuer() *Issuer { return a.issuer }

// Begin issues a ticket binding the authenticated subject, the negotiated
// subprotocol and the permitted frame types to the digest of req and the
// current connection generation, and opens a session for the upgrade.
func (a *Admitter) Begin(connID uint64, req *http.Request, subject, subprotocol string, allowedFrames []byte) (*UpgradeSession, error) {
	if req == nil {
		return nil, errors.New("upgrade session: nil request")
	}
	digest := DigestRequest(req)
	ticket := a.issuer.Issue(connID, digest, subject, subprotocol, allowedFrames, a.ttl)
	return &UpgradeSession{
		issuer:      a.issuer,
		connID:      connID,
		digest:      digest,
		subprotocol: subprotocol,
		ticket:      ticket,
	}, nil
}

// UpgradeSession tracks one upgrade attempt on a connection.
type UpgradeSession struct {
	issuer      *Issuer
	connID      uint64
	digest      [32]byte
	subprotocol string
	ticket      *Ticket

	upgraded bool
	aborted  bool
	preread  []byte
}

// Ticket returns the ticket backing the session.
func (s *UpgradeSession) Ticket() *Ticket { return s.ticket }

// CompleteUpgrade writes the 101 response. The pre-read buffer (bytes the
// HTTP parser already consumed from the connection beyond the request) is
// adopted only after the response has been written in full. On a short
// write the session is aborted and the connection generation advanced, so
// the ticket cannot be replayed when the connection is reused.
func (s *UpgradeSession) CompleteUpgrade(w io.Writer, response, preread []byte) error {
	if s.aborted {
		return ErrSessionAborted
	}
	if s.upgraded {
		return errors.New("upgrade session: already upgraded")
	}
	written := 0
	for written < len(response) {
		n, err := w.Write(response[written:])
		written += n
		if err != nil {
			s.abort()
			return fmt.Errorf("upgrade response incomplete (%d/%d bytes): %w", written, len(response), err)
		}
		if n == 0 {
			s.abort()
			return fmt.Errorf("upgrade response incomplete (%d/%d bytes): writer made no progress", written, len(response))
		}
	}
	s.preread = append([]byte(nil), preread...)
	s.upgraded = true
	return nil
}

func (s *UpgradeSession) abort() {
	s.aborted = true
	s.issuer.Advance(s.connID)
}

// ServeFrames parses frames from the pre-read buffer first and then from r,
// authorizing every frame against the ticket before dispatch. Rejected
// frames are reported via OnReject and never reach the data or management
// callbacks.
func (s *UpgradeSession) ServeFrames(r io.Reader, h FrameHandler) error {
	if s.aborted {
		return ErrSessionAborted
	}
	if !s.upgraded {
		return ErrNotUpgraded
	}
	src := io.MultiReader(bytes.NewReader(s.preread), r)
	s.preread = nil
	var header [3]byte
	for {
		if _, err := io.ReadFull(src, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading frame header: %w", err)
		}
		frameType := header[0]
		length := binary.BigEndian.Uint16(header[1:3])
		body := make([]byte, length)
		if _, err := io.ReadFull(src, body); err != nil {
			return fmt.Errorf("reading frame body: %w", err)
		}
		if err := s.authorize(frameType); err != nil {
			if h.OnReject != nil {
				h.OnReject(frameType, err)
			}
			continue
		}
		switch frameType {
		case FrameTypeData:
			if h.OnData != nil {
				h.OnData(body)
			}
		case FrameTypeManagement:
			if h.OnManagement != nil {
				h.OnManagement(body)
			}
		default:
			if h.OnReject != nil {
				h.OnReject(frameType, fmt.Errorf("unknown frame type %d", frameType))
			}
		}
	}
}

func (s *UpgradeSession) authorize(frameType byte) error {
	if err := s.issuer.Validate(s.ticket, s.connID, s.digest, s.subprotocol); err != nil {
		return err
	}
	if !s.ticket.Allows(frameType) {
		return fmt.Errorf("frame type %d not permitted for subject %q", frameType, s.ticket.Subject)
	}
	return nil
}
