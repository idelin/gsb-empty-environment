// Package netsec provides HelloRetryRequest group-validation helpers that a
// TLS stack can call while processing a server-triggered retry.
//
// Rules enforced (RFC 8446 section 4.1.2 and 4.2.8):
//   - The group the server selects in HelloRetryRequest must have been
//     offered in the original ClientHello's supported_groups extension.
//   - The second ClientHello's key_share extension must replace only that
//     one group: it carries exactly one KeyShareEntry, for the selected
//     group, and supported_groups must be identical to the first flight.
package netsec

import (
	"errors"
	"fmt"
)

// NamedGroup is a TLS 1.3 named group identifier.
type NamedGroup uint16

// KeyShareEntry is a single entry of a key_share extension.
type KeyShareEntry struct {
	Group NamedGroup
	Data  []byte
}

// ClientHello carries the fields of a ClientHello that matter for
// HelloRetryRequest validation.
type ClientHello struct {
	SupportedGroups []NamedGroup
	KeyShares       []KeyShareEntry
}

var (
	// ErrGroupNotOffered is returned when the HelloRetryRequest selects a
	// group that was absent from the first ClientHello's supported_groups.
	ErrGroupNotOffered = errors.New("netsec: HelloRetryRequest selected a group not offered in the original ClientHello")

	// ErrKeyShareNotReplaced is returned when the second ClientHello's
	// key_share is not exactly one entry for the selected group.
	ErrKeyShareNotReplaced = errors.New("netsec: second ClientHello key_share must replace only the selected group")

	// ErrSupportedGroupsChanged is returned when the second ClientHello
	// alters supported_groups, which a retry must not do.
	ErrSupportedGroupsChanged = errors.New("netsec: second ClientHello changed supported_groups")
)

// ValidateHelloRetryRequest checks that the group selected by the server's
// HelloRetryRequest was offered in the original ClientHello. It returns
// ErrGroupNotOffered otherwise.
func ValidateHelloRetryRequest(ch1 *ClientHello, selected NamedGroup) error {
	if ch1 == nil {
		return fmt.Errorf("%w: no original ClientHello", ErrGroupNotOffered)
	}
	for _, g := range ch1.SupportedGroups {
		if g == selected {
			return nil
		}
	}
	return fmt.Errorf("%w: group 0x%04x", ErrGroupNotOffered, uint16(selected))
}

// ValidateRetryClientHello checks the second ClientHello sent after a
// HelloRetryRequest: supported_groups must be unchanged and key_share must
// contain exactly one entry, for the group the server selected.
func ValidateRetryClientHello(ch1, ch2 *ClientHello, selected NamedGroup) error {
	if ch1 == nil || ch2 == nil {
		return fmt.Errorf("%w: missing ClientHello", ErrKeyShareNotReplaced)
	}
	if !equalGroups(ch1.SupportedGroups, ch2.SupportedGroups) {
		return ErrSupportedGroupsChanged
	}
	if len(ch2.KeyShares) != 1 || ch2.KeyShares[0].Group != selected {
		return fmt.Errorf("%w: got %d shares", ErrKeyShareNotReplaced, len(ch2.KeyShares))
	}
	if len(ch2.KeyShares[0].Data) == 0 {
		return fmt.Errorf("%w: empty key share for group 0x%04x", ErrKeyShareNotReplaced, uint16(selected))
	}
	return nil
}

// AuthorizeHandshake performs the full HelloRetryRequest retry check. A nil
// return means the retry is compliant and the TLS stack may proceed with the
// handshake using the re-sent share for the selected group.
func AuthorizeHandshake(ch1, ch2 *ClientHello, selected NamedGroup) error {
	if err := ValidateHelloRetryRequest(ch1, selected); err != nil {
		return err
	}
	return ValidateRetryClientHello(ch1, ch2, selected)
}

// BuildRetryKeyShare produces the key_share extension for the second
// ClientHello: a single entry replacing only the selected group.
func BuildRetryKeyShare(selected NamedGroup, keyData []byte) []KeyShareEntry {
	return []KeyShareEntry{{Group: selected, Data: keyData}}
}

func equalGroups(a, b []NamedGroup) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
