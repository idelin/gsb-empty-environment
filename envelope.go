package netsec

import (
	"fmt"
)

// Envelope contains SMTP envelope addresses. It is deliberately separate from
// message headers: display names, address lists, comments, and non-ASCII text
// are rejected here even when the same value could be represented in a To or
// From header.
type Envelope struct {
	From       string
	Recipients []string
}

// NewEnvelope validates a reverse-path and every recipient as an individual
// bare ASCII addr-spec. The returned value owns a copy of the recipient slice.
func NewEnvelope(from string, recipients []string) (Envelope, error) {
	if err := validateMailbox(from); err != nil {
		return Envelope{}, fmt.Errorf("envelope sender: %w", err)
	}
	if len(recipients) == 0 {
		return Envelope{}, fmt.Errorf("%w: envelope has no recipients", ErrInvalidAddress)
	}
	copied := make([]string, len(recipients))
	for i, recipient := range recipients {
		if err := validateMailbox(recipient); err != nil {
			return Envelope{}, fmt.Errorf("envelope recipient %d: %w", i+1, err)
		}
		copied[i] = recipient
	}
	return Envelope{From: from, Recipients: copied}, nil
}

// Validate re-checks the envelope, including values changed through the
// exported fields after construction.
func (e Envelope) Validate() error {
	if err := validateMailbox(e.From); err != nil {
		return fmt.Errorf("envelope sender: %w", err)
	}
	if len(e.Recipients) == 0 {
		return fmt.Errorf("%w: envelope has no recipients", ErrInvalidAddress)
	}
	for i, recipient := range e.Recipients {
		if err := validateMailbox(recipient); err != nil {
			return fmt.Errorf("envelope recipient %d: %w", i+1, err)
		}
	}
	return nil
}

// RcptTo returns a copy of the validated SMTP RCPT TO values.
func (e Envelope) RcptTo() ([]string, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return append([]string(nil), e.Recipients...), nil
}

// ValidateEnvelopeRecipients validates only an SMTP recipient list.
func ValidateEnvelopeRecipients(recipients []string) error {
	_, err := NewEnvelope("postmaster@example.invalid", recipients)
	return err
}
