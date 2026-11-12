package netsec

import (
	"errors"
	"fmt"
	"io"
)

// Session is a connection that has completed ZMTP greeting negotiation.
// Use ReadFrame for every inbound frame; it automatically enforces CURVE
// nonce monotonicity for MESSAGE commands.
type Session struct {
	rw           io.ReadWriter
	role         Role
	mechanism    Mechanism
	peer         Greeting
	maxFrameSize int64
	nonces       NonceTracker
}

// Handshake exchanges greetings over rw and enforces the local security
// configuration. On success the peer greeting is returned in the Session.
// On any protocol violation the method fails closed and closes rw when it
// implements io.Closer.
func Handshake(rw io.ReadWriter, cfg Config) (*Session, error) {
	if rw == nil {
		return nil, fmt.Errorf("%w: nil connection", ErrMalformedGreeting)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	local, err := cfg.localGreeting()
	if err != nil {
		return nil, err
	}
	localBytes, err := local.MarshalBinary()
	if err != nil {
		return nil, err
	}

	peerBytes, err := exchangeGreeting(rw, localBytes)
	if err != nil {
		return nil, closeOnError(rw, err)
	}

	peer, err := ParseGreeting(peerBytes)
	if err != nil {
		return nil, closeOnError(rw, err)
	}
	if err := cfg.ValidatePeer(peer); err != nil {
		return nil, closeOnError(rw, err)
	}

	return &Session{
		rw:           rw,
		role:         cfg.Role,
		mechanism:    cfg.Mechanism,
		peer:         peer,
		maxFrameSize: cfg.effectiveMaxFrameSize(),
	}, nil
}

// ServerHandshake is Handshake with cfg.Role forced to RoleServer.
func ServerHandshake(rw io.ReadWriter, cfg Config) (*Session, error) {
	cfg.Role = RoleServer
	return Handshake(rw, cfg)
}

// ClientHandshake is Handshake with cfg.Role forced to RoleClient.
func ClientHandshake(rw io.ReadWriter, cfg Config) (*Session, error) {
	cfg.Role = RoleClient
	return Handshake(rw, cfg)
}

func exchangeGreeting(rw io.ReadWriter, local []byte) ([]byte, error) {
	writeDone := make(chan error, 1)
	go func() {
		err := writeFull(rw, local)
		writeDone <- err
	}()

	peer, readErr := readGreetingBytes(rw)
	if readErr != nil {
		// Closing unblocks the greeting writer. Do not wait for it: a
		// malicious peer may deliberately refuse to drain our greeting.
		if closer, ok := rw.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, readErr
	}

	writeErr := <-writeDone
	if writeErr != nil {
		return nil, fmt.Errorf("netsec: write greeting: %w", writeErr)
	}
	return peer, nil
}

// Peer returns the validated peer greeting.
func (s *Session) Peer() Greeting {
	return s.peer
}

// Role returns the local role.
func (s *Session) Role() Role {
	return s.role
}

// Mechanism returns the negotiated mechanism.
func (s *Session) Mechanism() Mechanism {
	return s.mechanism
}

// LastNonce returns the most recently accepted inbound CURVE nonce.
func (s *Session) LastNonce() (uint64, bool) {
	return s.nonces.Last()
}

// ReadFrame reads one frame. For CURVE sessions, any body that parses as
// a MESSAGE command updates the nonce tracker, independent of the frame's
// command flag. A repeated or decreasing nonce closes the connection and
// returns ErrNonceReplay. Malformed CURVE MESSAGE command bodies also fail
// closed.
func (s *Session) ReadFrame() (Frame, error) {
	frame, err := ReadFrame(s.rw, s.maxFrameSize)
	if err != nil {
		return Frame{}, s.fail(err)
	}

	if s.mechanism == MechanismCurve {
		message, err := ParseCurveMessage(frame.Body)
		if err == nil {
			if err := s.nonces.Check(message.Nonce); err != nil {
				return Frame{}, s.fail(err)
			}
		} else if !errors.Is(err, ErrNotCurveMessage) {
			return Frame{}, s.fail(err)
		}
	}
	return frame, nil
}

// ReadCurveMessage reads a CURVE MESSAGE command and returns its nonce
// and encrypted box. The nonce has already been accepted by the session
// tracker.
func (s *Session) ReadCurveMessage() (CurveMessage, error) {
	if s.mechanism != MechanismCurve {
		return CurveMessage{}, fmt.Errorf("%w: session mechanism is %q", ErrNotCurveMessage, s.mechanism)
	}
	frame, err := s.ReadFrame()
	if err != nil {
		return CurveMessage{}, err
	}
	message, err := ParseCurveMessage(frame.Body)
	if err != nil {
		return CurveMessage{}, err
	}
	return message, nil
}

// WriteFrame writes one frame to the peer.
func (s *Session) WriteFrame(frame Frame) error {
	return WriteFrame(s.rw, frame)
}

// Close closes the underlying connection when it implements io.Closer.
func (s *Session) Close() error {
	if closer, ok := s.rw.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (s *Session) fail(err error) error {
	if closer, ok := s.rw.(io.Closer); ok {
		_ = closer.Close()
	}
	return err
}

func closeOnError(rw io.ReadWriter, err error) error {
	if closer, ok := rw.(io.Closer); ok {
		_ = closer.Close()
	}
	return err
}
