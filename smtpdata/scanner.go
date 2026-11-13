// Package smtpdata scans the payload of an inbound SMTP DATA phase.
//
// SMTP ends DATA with the five-byte sequence CRLF dot CRLF ("\r\n.\r\n").
// When two SMTP peers disagree about what counts as that end-of-data marker,
// an attacker can embed a bare line ending followed by a dot line (for
// example "\n.\r\n" or "\r.\r\n") to make one peer terminate the message
// early and parse the remainder as a new SMTP command, forging mail from an
// arbitrary sender. This is the SMTP smuggling class of bugs.
//
// Scanner implements the strict reading rules required by RFC 5321:
//
//   - The only accepted end-of-data marker is the exact sequence
//     "\r\n.\r\n" (".\r\n" at the very start of the payload is the same
//     marker with no preceding message bytes).
//   - A bare LF ('\n' not immediately preceded by '\r') or a bare CR
//     ('\r' not immediately followed by '\n') anywhere in the payload is a
//     fatal protocol error. The whole message is rejected; line endings are
//     never normalized.
//   - Dot stuffing is reversed: a line beginning with ".." is delivered
//     with one leading dot removed.
//   - Input may be supplied in blocks of any size. A block boundary may
//     fall between any two bytes, including the CR and LF of a line
//     ending, and the result is identical to feeding the whole payload at
//     once.
//   - The accumulated decoded message is rejected once it exceeds a
//     caller-supplied size limit.
//
// Scanner is an io.Writer. It is not safe for concurrent use.
package smtpdata

import "errors"

// Sentinel errors returned by Scanner.Write and Scanner.Err. All failures
// are terminal: once Write reports an error, the scanner must be discarded
// or reset before it can accept another message.
var (
	// ErrBareLF means a '\n' byte appeared without a preceding '\r'.
	ErrBareLF = errors.New("smtpdata: bare LF in DATA payload")

	// ErrBareCR means a '\r' byte was not followed by '\n'.
	ErrBareCR = errors.New("smtpdata: bare CR in DATA payload")

	// ErrTooLarge means the decoded message reached the configured limit.
	ErrTooLarge = errors.New("smtpdata: DATA payload exceeds size limit")

	// ErrTrailingData means bytes were received after the end-of-data
	// marker. In a healthy SMTP session those bytes would belong to the
	// next command instead of DATA; accepting them silently would hide
	// desynchronization between peers.
	ErrTrailingData = errors.New("smtpdata: trailing bytes after end-of-data marker")
)

// errDone is the unexported signal that feed matched the end-of-data marker.
var errDone = errors.New("smtpdata: end of DATA")

// parser states. CR and the line-start dot are held back until the next
// byte decides whether they complete CRLF / the terminator or are illegal.
const (
	stateStart = iota // at the beginning of a line, nothing held
	stateData         // inside a line
	stateCR           // mid-line '\r' held, waiting for '\n'
	stateDot          // line-start '.' held, waiting for '.' / '\r' / other
	stateDotCR        // line-start ".\r" held, waiting for terminating '\n'
)

// Scanner accumulates the bytes of one SMTP DATA payload.
type Scanner struct {
	limit int64 // < 0 means unlimited
	size  int64 // decoded message bytes committed so far
	state uint8
	done  bool
	err   error
	buf   []byte
}

// NewScanner returns a Scanner that rejects a message once its decoded size
// exceeds limit bytes. A negative limit disables the cap; a limit of zero
// accepts only an empty message.
func NewScanner(limit int64) *Scanner {
	return &Scanner{limit: limit}
}

// Write feeds the next block of DATA bytes. It always consumes the whole
// block (n == len(p)); a non-nil error means the scanner has entered a
// terminal state and the message must be rejected.
func (s *Scanner) Write(p []byte) (int, error) {
	if s.err != nil {
		return len(p), s.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.done {
		s.err = ErrTrailingData
		return len(p), s.err
	}
	for i := 0; i < len(p); i++ {
		err := s.feed(p[i])
		if err == errDone {
			s.done = true
			if i+1 < len(p) {
				s.err = ErrTrailingData
				return len(p), s.err
			}
			return len(p), nil
		}
		if err != nil {
			s.err = err
			return len(p), err
		}
	}
	return len(p), nil
}

// feed processes one wire byte. Bytes of the terminator sequence are never
// emitted: they are held in stateDot/stateDotCR and dropped on completion.
func (s *Scanner) feed(b byte) error {
	switch s.state {
	case stateStart:
		switch b {
		case '\r':
			s.state = stateCR
		case '\n':
			return ErrBareLF
		case '.':
			s.state = stateDot
		default:
			if err := s.emit(b); err != nil {
				return err
			}
			s.state = stateData
		}

	case stateData:
		switch b {
		case '\r':
			s.state = stateCR
		case '\n':
			return ErrBareLF
		default:
			if err := s.emit(b); err != nil {
				return err
			}
		}

	case stateCR:
		// A '\r' is held pending the next byte, so it can only be legal
		// as the first half of CRLF.
		if b != '\n' {
			return ErrBareCR
		}
		if err := s.emitCRLF(); err != nil {
			return err
		}
		s.state = stateStart

	case stateDot:
		switch b {
		case '.':
			// Dot stuffing: ".." at line start decodes to one '.'.
			if err := s.emit('.'); err != nil {
				return err
			}
			s.state = stateData
		case '\r':
			s.state = stateDotCR
		case '\n':
			// The held dot would be content, but this LF is bare.
			return ErrBareLF
		default:
			// The held dot was ordinary content; commit it and this byte.
			if err := s.emit('.'); err != nil {
				return err
			}
			if err := s.emit(b); err != nil {
				return err
			}
			s.state = stateData
		}

	case stateDotCR:
		// ".\r" at line start is held; only LF completes the terminator.
		if b != '\n' {
			return ErrBareCR
		}
		return errDone
	}
	return nil
}

// emit appends one decoded message byte, enforcing the size limit first.
func (s *Scanner) emit(b byte) error {
	if s.limit >= 0 && s.size >= s.limit {
		return ErrTooLarge
	}
	s.buf = append(s.buf, b)
	s.size++
	return nil
}

// emitCRLF appends a validated CRLF line ending.
func (s *Scanner) emitCRLF() error {
	if err := s.emit('\r'); err != nil {
		return err
	}
	return s.emit('\n')
}

// Done reports whether the complete end-of-data marker has been consumed.
func (s *Scanner) Done() bool {
	return s.done
}

// Err returns the terminal error that stopped the scanner, or nil while the
// message is still being received or was completed cleanly.
func (s *Scanner) Err() error {
	return s.err
}

// Body returns the decoded, dot-unstuffed message with its original CRLF
// line endings. The returned slice is owned by the scanner and remains
// valid only until the next Write or Reset call.
func (s *Scanner) Body() []byte {
	return s.buf
}

// Size returns the number of decoded message bytes accumulated so far. The
// five-byte end-of-data marker is not counted.
func (s *Scanner) Size() int64 {
	return s.size
}

// Reset returns the scanner to its initial state, keeping the configured
// size limit, so it can be reused for a new DATA payload.
func (s *Scanner) Reset() {
	s.buf = s.buf[:0]
	s.size = 0
	s.state = stateStart
	s.done = false
	s.err = nil
}
