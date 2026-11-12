package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidJID is returned when a JID does not have a valid localpart,
// domainpart, or resourcepart structure.
var ErrInvalidJID = errors.New("invalid JID")

// JID is the parsed form of an XMPP address.
//
// A JID has the form localpart@domainpart/resourcepart.  The localpart and
// resourcepart are optional; the domainpart is mandatory.
type JID struct {
	Local    string
	Domain   string
	Resource string
}

// ParseJID parses an XMPP address.  It accepts domain-only JIDs, bare JIDs,
// and full JIDs.  Whitespace and malformed addresses are rejected.
func ParseJID(raw string) (JID, error) {
	if raw == "" {
		return JID{}, fmt.Errorf("%w: empty address", ErrInvalidJID)
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return JID{}, fmt.Errorf("%w: whitespace in address", ErrInvalidJID)
	}

	nodeDomain := raw
	var resource string
	if slash := strings.IndexByte(raw, '/'); slash >= 0 {
		nodeDomain = raw[:slash]
		resource = raw[slash+1:]
		if resource == "" {
			return JID{}, fmt.Errorf("%w: empty resourcepart", ErrInvalidJID)
		}
	}

	var local string
	domain := nodeDomain
	if at := strings.LastIndexByte(nodeDomain, '@'); at >= 0 {
		local = nodeDomain[:at]
		domain = nodeDomain[at+1:]
		if local == "" {
			return JID{}, fmt.Errorf("%w: empty localpart", ErrInvalidJID)
		}
		if strings.ContainsRune(local, '@') {
			return JID{}, fmt.Errorf("%w: unescaped @ in localpart", ErrInvalidJID)
		}
	}
	if domain == "" {
		return JID{}, fmt.Errorf("%w: empty domainpart", ErrInvalidJID)
	}

	return JID{Local: local, Domain: domain, Resource: resource}, nil
}

// Bare returns the JID without its resourcepart.
func (j JID) Bare() string {
	if j.Local == "" {
		return j.Domain
	}
	return j.Local + "@" + j.Domain
}

// Full returns the JID including its resourcepart when present.
func (j JID) Full() string {
	if j.Resource == "" {
		return j.Bare()
	}
	return j.Bare() + "/" + j.Resource
}

// IsBare reports whether the JID has no resourcepart.
func (j JID) IsBare() bool {
	return j.Resource == ""
}

func (j JID) sameBare(other JID) bool {
	return strings.EqualFold(j.Local, other.Local) &&
		strings.EqualFold(j.Domain, other.Domain)
}

func (j JID) sameFull(other JID) bool {
	return j.sameBare(other) && j.Resource == other.Resource
}
