package starttls

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// SMTP reply code for "go ahead with STARTTLS" (RFC 3207).
const smtpReadyTLS = 220

// UpgradeRejectedError reports that the peer refused STARTTLS instead of
// issuing the 220 go-ahead. The relay must remain in the plaintext phase and
// decide whether to continue or disconnect; Engine state is unchanged.
type UpgradeRejectedError struct {
	// Reply is the full refusal line without its CRLF.
	Reply string
	// Code is the parsed three-digit reply code, or 0 when unparseable.
	Code int
}

func (e *UpgradeRejectedError) Error() string {
	return "starttls: upgrade rejected by peer: " + e.Reply
}

// IsSTARTTLS reports whether line is an SMTP STARTTLS command. The verb is
// case-insensitive and no parameters are allowed.
func IsSTARTTLS(line string) bool {
	return strings.EqualFold(line, "STARTTLS")
}

// ResponseCode parses an SMTP reply line ("220 ready") and returns its
// three-digit code. Multiline continuation replies (e.g. "220-...") are
// accepted as well. A malformed line yields 0.
func ResponseCode(line string) int {
	if len(line) < 3 {
		return 0
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil || code < 100 || code > 599 {
		return 0
	}
	return code
}

// IsPositiveCompletion reports whether code is a 2xx reply.
func IsPositiveCompletion(code int) bool {
	return code >= 200 && code <= 299
}

// ClientStartTLS drives the client side of the STARTTLS handshake:
//
//  1. send the STARTTLS command,
//  2. read the server reply,
//  3. on a 220 go-ahead run BeginUpgrade with upgrade,
//     otherwise return *UpgradeRejectedError without changing phase.
//
// It is a convenience wrapper around Engine.ReadLine/WriteLine/BeginUpgrade
// so relays keep one consistent boundary implementation on both sides.
func (e *Engine) ClientStartTLS(ctx context.Context, upgrade UpgradeFunc) error {
	if err := e.WriteLine("STARTTLS"); err != nil {
		return err
	}
	reply, err := e.ReadLine()
	if err != nil {
		return err
	}
	code := ResponseCode(reply)
	if code != smtpReadyTLS {
		return &UpgradeRejectedError{Reply: reply, Code: code}
	}
	return e.BeginUpgrade(ctx, upgrade)
}

// ServerBeginUpgrade is the server-side boundary call used after the relay
// has itself read a STARTTLS command and written the 220 go-ahead. It applies
// the same pipelining and buffer-scrubbing rules as the client side.
func (e *Engine) ServerBeginUpgrade(ctx context.Context, upgrade UpgradeFunc) error {
	return e.BeginUpgrade(ctx, upgrade)
}

// AsUpgradeRejected unwraps an *UpgradeRejectedError, if present.
func AsUpgradeRejected(err error) (*UpgradeRejectedError, bool) {
	var rejected *UpgradeRejectedError
	if errors.As(err, &rejected) {
		return rejected, true
	}
	return nil, false
}
