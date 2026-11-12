package netsec

import (
	"crypto/sha256"
	"fmt"
	"net"
)

// Commit is the security-relevant portion of an SAE Commit frame.
type Commit struct {
	Group   GroupID
	Scalar  []byte
	Element []byte
}

// Validate checks the scalar, the element, and the optional AP own element.
func (c Commit) Validate(ownElement []byte) error {
	g, ok := LookupGroup(c.Group)
	if !ok {
		return ErrUnsupportedGroup
	}
	return g.ValidateCommit(c.Scalar, c.Element, ownElement)
}

// CommitRequest is the input to Filter.HandleCommit. APElement is optional;
// when empty, the filter uses the AP element configured for the group. A
// non-empty APElement lets the Authentication handler pass the AP's
// per-exchange public value.
type CommitRequest struct {
	StationMAC net.HardwareAddr
	Group      GroupID
	Scalar     []byte
	Element    []byte
	Token      []byte
	APElement  []byte
}

// AuthenticationRequest is an alias for CommitRequest for callers that model
// the whole Authentication frame.
type AuthenticationRequest = CommitRequest

// ParseCommitBody splits the scalar and element fields from the portion of an
// Authentication frame body that follows the group identifier. The layout is
// scalar || element, both using the group's fixed widths.
func ParseCommitBody(group GroupID, body []byte) (Commit, error) {
	g, ok := LookupGroup(group)
	if !ok {
		return Commit{}, ErrUnsupportedGroup
	}
	want := g.ScalarSize() + g.ElementSize()
	if len(body) != want {
		return Commit{}, fmt.Errorf("%w: got %d bytes, want %d", ErrElementEncoding, len(body), want)
	}
	return Commit{
		Group:   group,
		Scalar:  append([]byte(nil), body[:g.ScalarSize()]...),
		Element: append([]byte(nil), body[g.ScalarSize():]...),
	}, nil
}

func commitFingerprint(group GroupID, scalar, element []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte("netsec/sae-commit/v1"))
	g := groupBytes(group)
	h.Write(g[:])
	h.Write(scalar)
	h.Write(element)

	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}
