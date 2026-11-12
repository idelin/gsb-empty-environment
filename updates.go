package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ParseUpdates reads ref updates in the format used on a pre-receive hook's
// standard input: one "<old-oid> <new-oid> <ref>" line per update.
func ParseUpdates(r io.Reader) ([]Update, error) {
	scanner := bufio.NewScanner(r)
	var updates []Update
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		update, ok := parseUpdateLine(line)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMalformedUpdate, line)
		}
		updates = append(updates, update)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("netsec: read ref updates: %w", err)
	}
	return updates, nil
}

// ParseUpdateText parses ref updates from a byte slice. See ParseUpdates.
func ParseUpdateText(data []byte) ([]Update, error) {
	return ParseUpdates(bytes.NewReader(data))
}

// CompareUpdates verifies that the certificate updates and the updates
// received by the hook are identical item by item, including their order and
// count. Either a missing or an extra update is rejected.
func CompareUpdates(certificateUpdates, receivedUpdates []Update) error {
	if len(certificateUpdates) != len(receivedUpdates) {
		return &UpdateMismatchError{
			Index:            -1,
			CertificateCount: len(certificateUpdates),
			ReceivedCount:    len(receivedUpdates),
		}
	}
	for i := range certificateUpdates {
		if !certificateUpdates[i].Equal(receivedUpdates[i]) {
			certificateUpdate := certificateUpdates[i]
			receivedUpdate := receivedUpdates[i]
			return &UpdateMismatchError{
				Index:            i,
				Certificate:      &certificateUpdate,
				Received:         &receivedUpdate,
				CertificateCount: len(certificateUpdates),
				ReceivedCount:    len(receivedUpdates),
			}
		}
	}
	return nil
}

// UpdateMismatchError describes a certificate update that does not exactly
// match the update received by the hook. Index is -1 when the number of
// updates differs.
type UpdateMismatchError struct {
	Index            int
	Certificate      *Update
	Received         *Update
	CertificateCount int
	ReceivedCount    int
}

func (e *UpdateMismatchError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf(
			"netsec: certificate has %d updates but hook received %d",
			e.CertificateCount, e.ReceivedCount,
		)
	}
	return fmt.Sprintf(
		"netsec: update %d differs: certificate has %q, hook received %q",
		e.Index, e.Certificate.String(), e.Received.String(),
	)
}

func (e *UpdateMismatchError) Unwrap() error {
	return ErrUpdateMismatch
}

// ErrMalformedUpdate indicates an update line that is not an
// "<old-oid> <new-oid> <ref>" tuple.
var ErrMalformedUpdate = errors.New("netsec: malformed ref update")

// ErrUpdateMismatch indicates that the certificate's updates do not exactly
// match the updates received by the hook.
var ErrUpdateMismatch = errors.New("netsec: certificate updates do not match push")
