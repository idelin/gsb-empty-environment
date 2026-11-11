package nameconstraint

import (
	"crypto/x509"
	"encoding/asn1"
	"net"
)

var oidExtensionSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
var oidExtensionNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

// GeneralName context tags from RFC 5280 4.2.1.6.
const (
	tagRFC822Name = 1
	tagDNSName    = 2
	tagIPAddress  = 7
)

type nameConstraints struct {
	permittedDNS    []string
	excludedDNS     []string
	permittedIP     []*net.IPNet
	excludedIP      []*net.IPNet
	permittedEmail  []string
	excludedEmail   []string
	permittedDNSSet bool
	permittedIPSet  bool
	permittedMail   bool
}

// parseNameConstraints extracts and decodes the NameConstraints extension of
// cert. present is false when the extension does not exist. An extension that
// is present but contains an empty permitted-subtree field is preserved so
// that it can deny all names of the corresponding type.
func parseNameConstraints(cert *x509.Certificate) (nc *nameConstraints, present bool, err error) {
	var ext []byte
	for _, e := range cert.Extensions {
		if e.Id.Equal(oidExtensionNameConstraints) {
			ext = e.Value
			break
		}
	}
	if ext == nil {
		return nil, false, nil
	}

	// NameConstraints ::= SEQUENCE {
	//      permittedSubtrees       [0]     GeneralSubtrees OPTIONAL,
	//      excludedSubtrees        [1]     GeneralSubtrees OPTIONAL }
	var raw struct {
		Permitted asn1.RawValue `asn1:"optional,tag:0,explicit"`
		Excluded  asn1.RawValue `asn1:"optional,tag:1,explicit"`
	}
	if rest, perr := asn1.Unmarshal(ext, &raw); perr != nil {
		return nil, true, perr
	} else if len(rest) != 0 {
		return nil, true, asn1.SyntaxError{Msg: "trailing data after NameConstraints"}
	}

	nc = &nameConstraints{}
	if len(raw.Permitted.FullBytes) > 0 {
		if err := parseSubtrees(raw.Permitted.Bytes, nc, true); err != nil {
			return nil, true, err
		}
		// An explicitly empty permittedSubtrees denies every name of every
		// constrained type: the type-specific set flags were never raised.
		if !nc.permittedDNSSet && !nc.permittedIPSet && !nc.permittedMail {
			nc.permittedDNSSet = true
			nc.permittedIPSet = true
			nc.permittedMail = true
		}
	}
	if len(raw.Excluded.FullBytes) > 0 {
		if err := parseSubtrees(raw.Excluded.Bytes, nc, false); err != nil {
			return nil, true, err
		}
	}
	return nc, true, nil
}

func parseSubtrees(data []byte, nc *nameConstraints, permitted bool) error {
	// data is the content of the [0]/[1] GeneralSubtrees value, i.e. a
	// SEQUENCE OF GeneralSubtree.
	var seq asn1.RawValue
	if rest, err := asn1.Unmarshal(data, &seq); err != nil {
		return err
	} else if len(rest) != 0 {
		return asn1.SyntaxError{Msg: "trailing data after GeneralSubtrees"}
	}

	inner := seq.Bytes
	for len(inner) > 0 {
		// GeneralSubtree ::= SEQUENCE { base GeneralName, ... }
		var subtree asn1.RawValue
		var rest []byte
		var err error
		if rest, err = asn1.Unmarshal(inner, &subtree); err != nil {
			return err
		}
		inner = rest

		// The first element of the subtree is the implicitly tagged
		// GeneralName; minimum/maximum fields that may follow are ignored.
		var base asn1.RawValue
		if _, err := asn1.Unmarshal(subtree.Bytes, &base); err != nil {
			return err
		}
		tag := base.Tag
		value := base.Bytes
		switch tag {
		case tagDNSName:
			if permitted {
				nc.permittedDNSSet = true
				nc.permittedDNS = append(nc.permittedDNS, string(value))
			} else {
				nc.excludedDNS = append(nc.excludedDNS, string(value))
			}
		case tagRFC822Name:
			if permitted {
				nc.permittedMail = true
				nc.permittedEmail = append(nc.permittedEmail, string(value))
			} else {
				nc.excludedEmail = append(nc.excludedEmail, string(value))
			}
		case tagIPAddress:
			ipnet, err := parseIPConstraint(value)
			if err != nil {
				return err
			}
			if permitted {
				nc.permittedIPSet = true
				nc.permittedIP = append(nc.permittedIP, ipnet)
			} else {
				nc.excludedIP = append(nc.excludedIP, ipnet)
			}
		default:
			// Directory names, URIs and other name forms are out of scope;
			// RFC 5280 permits an implementation to ignore unsupported forms.
		}
	}
	return nil
}

// parseIPConstraint decodes the 8 (IPv4) or 16 (IPv6) octet
// address/mask pair carried by an iPAddress name constraint.
func parseIPConstraint(value []byte) (*net.IPNet, error) {
	var ipLen int
	switch len(value) {
	case 2 * net.IPv4len:
		ipLen = net.IPv4len
	case 2 * net.IPv6len:
		ipLen = net.IPv6len
	default:
		return nil, asn1.SyntaxError{Msg: "invalid IP name constraint length"}
	}
	ip := net.IP(append([]byte(nil), value[:ipLen]...))
	mask := net.IPMask(append([]byte(nil), value[ipLen:]...))
	if !validIPMask(mask) {
		return nil, asn1.SyntaxError{Msg: "invalid IP name constraint mask"}
	}
	return &net.IPNet{IP: ip, Mask: mask}, nil
}

func validIPMask(mask net.IPMask) bool {
	seenZero := false
	for _, b := range mask {
		for bit := 7; bit >= 0; bit-- {
			set := b&(1<<uint(bit)) != 0
			if seenZero && set {
				return false
			}
			if !set {
				seenZero = true
			}
		}
	}
	return true
}
