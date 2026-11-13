package netsec

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
)

// NameType is the GeneralName CHOICE tag from RFC 5280 section 4.2.1.6.
type NameType uint8

// GeneralNameType is an alias for NameType.
type GeneralNameType = NameType

// RegisteredID is the OID type used by GeneralName registeredID values.
type RegisteredID = asn1.ObjectIdentifier

const (
	NameTypeOtherName     NameType = 0
	NameTypeEmail         NameType = 1
	NameTypeDNS           NameType = 2
	NameTypeX400Address   NameType = 3
	NameTypeDirectoryName NameType = 4
	NameTypeEDIPartyName  NameType = 5
	NameTypeURI           NameType = 6
	NameTypeIPAddress     NameType = 7
	NameTypeRegisteredID  NameType = 8
)

// Descriptive aliases for callers that prefer GeneralName-prefixed names.
const (
	GeneralNameOtherName     = NameTypeOtherName
	GeneralNameEmail         = NameTypeEmail
	GeneralNameDNS           = NameTypeDNS
	GeneralNameX400Address   = NameTypeX400Address
	GeneralNameDirectoryName = NameTypeDirectoryName
	GeneralNameEDIPartyName  = NameTypeEDIPartyName
	GeneralNameURI           = NameTypeURI
	GeneralNameIPAddress     = NameTypeIPAddress
	GeneralNameRegisteredID  = NameTypeRegisteredID
)

func (t NameType) String() string {
	switch t {
	case NameTypeOtherName:
		return "otherName"
	case NameTypeEmail:
		return "rfc822Name"
	case NameTypeDNS:
		return "dNSName"
	case NameTypeX400Address:
		return "x400Address"
	case NameTypeDirectoryName:
		return "directoryName"
	case NameTypeEDIPartyName:
		return "ediPartyName"
	case NameTypeURI:
		return "uniformResourceIdentifier"
	case NameTypeIPAddress:
		return "iPAddress"
	case NameTypeRegisteredID:
		return "registeredID"
	default:
		return fmt.Sprintf("GeneralName(%d)", uint8(t))
	}
}

// GeneralName is one parsed GeneralName value.
//
// Value holds the IA5String form for email, DNS, and URI names, the textual
// IP form for IP names, and is otherwise informational. OID is set for
// registeredID names. Raw is the original GeneralName content (the bytes
// inside the context-specific tag).
type GeneralName struct {
	Type  NameType
	Value string
	OID   asn1.ObjectIdentifier
	IP    net.IP
	Mask  net.IPMask
	Raw   []byte
}

func (n GeneralName) String() string {
	if n.Type == NameTypeRegisteredID {
		if len(n.OID) > 0 {
			return n.OID.String()
		}
		return "<invalid registeredID>"
	}
	return n.Value
}

// IsRegisteredID reports whether n is a registeredID GeneralName.
func (n GeneralName) IsRegisteredID() bool {
	return n.Type == NameTypeRegisteredID && len(n.OID) > 0
}

// ParseOID parses a dotted-decimal object identifier. Every arc must be a
// non-negative decimal integer without a plus sign or leading zeroes. The
// first arc must be 0, 1, or 2.
func ParseOID(s string) (asn1.ObjectIdentifier, error) {
	if s == "" {
		return nil, errors.New("empty OID")
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("OID %q has fewer than two arcs", s)
	}
	oid := make(asn1.ObjectIdentifier, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("OID %q has an empty arc", s)
		}
		if len(p) > 1 && p[0] == '0' {
			return nil, fmt.Errorf("OID %q has a leading zero", s)
		}
		var v uint64
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("OID %q contains a non-decimal arc", s)
			}
			d := uint64(c - '0')
			if v > (math.MaxInt64-d)/10 {
				return nil, fmt.Errorf("OID %q arc overflows", s)
			}
			v = v*10 + d
		}
		oid[i] = int(v)
	}
	if oid[0] > 2 {
		return nil, fmt.Errorf("OID %q has invalid first arc %d", s, oid[0])
	}
	if oid[0] < 2 && oid[1] >= 40 {
		return nil, fmt.Errorf("OID %q has invalid second arc %d", s, oid[1])
	}
	return oid, nil
}

// OIDEqual reports whether two object identifiers have identical arcs.
// Encoding differences are not relevant once decoded, but this function
// intentionally compares the complete sequence rather than a text prefix.
func OIDEqual(a, b asn1.ObjectIdentifier) bool {
	return a.Equal(b)
}

// decodeOIDContent decodes the contents octets of a DER OBJECT IDENTIFIER.
func decodeOIDContent(b []byte) (asn1.ObjectIdentifier, error) {
	if len(b) == 0 {
		return nil, errors.New("empty OID content")
	}
	var subIDs []uint64
	for i := 0; i < len(b); {
		if b[i] == 0x80 {
			return nil, errors.New("OID has a non-minimal subidentifier")
		}
		var v uint64
		j := i
		for {
			if j >= len(b) {
				return nil, errors.New("OID subidentifier is truncated")
			}
			c := b[j]
			if v > (math.MaxInt64-uint64(c&0x7f))>>7 {
				return nil, errors.New("OID subidentifier overflows")
			}
			v = v<<7 | uint64(c&0x7f)
			j++
			if c&0x80 == 0 {
				break
			}
		}
		subIDs = append(subIDs, v)
		i = j
	}
	if len(subIDs) == 0 {
		return nil, errors.New("OID has no subidentifiers")
	}
	first := subIDs[0]
	var arc0, arc1 uint64
	switch {
	case first < 40:
		arc0, arc1 = 0, first
	case first < 80:
		arc0, arc1 = 1, first-40
	default:
		arc0, arc1 = 2, first-80
	}
	oid := make(asn1.ObjectIdentifier, 0, len(subIDs)+1)
	oid = append(oid, int(arc0), int(arc1))
	for _, v := range subIDs[1:] {
		if v > math.MaxInt {
			return nil, errors.New("OID arc overflows int")
		}
		oid = append(oid, int(v))
	}
	return oid, nil
}

// marshalOIDContent returns the content octets for a DER OBJECT IDENTIFIER.
func marshalOIDContent(oid asn1.ObjectIdentifier) ([]byte, error) {
	if len(oid) < 2 {
		return nil, errors.New("OID has fewer than two arcs")
	}
	if oid[0] < 0 || oid[0] > 2 || oid[1] < 0 || (oid[0] < 2 && oid[1] >= 40) {
		return nil, errors.New("OID has invalid arcs")
	}
	first := uint64(oid[0])*40 + uint64(oid[1])
	var out []byte
	appendSubID := func(v uint64) {
		var tmp [10]byte
		n := 0
		tmp[n] = byte(v & 0x7f)
		n++
		v >>= 7
		for v > 0 {
			tmp[n] = byte(v&0x7f) | 0x80
			n++
			v >>= 7
		}
		for i := n - 1; i >= 0; i-- {
			out = append(out, tmp[i])
		}
	}
	appendSubID(first)
	for _, arc := range oid[2:] {
		if arc < 0 {
			return nil, errors.New("OID has a negative arc")
		}
		appendSubID(uint64(arc))
	}
	return out, nil
}
