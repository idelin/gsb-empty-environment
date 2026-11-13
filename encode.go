package netsec

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
)

// MarshalNameConstraints encodes nc as the value of a nameConstraints
// extension. It is useful for constructing test CAs and for callers that need
// to combine registeredID constraints with the fields supported by
// crypto/x509.
func MarshalNameConstraints(nc *NameConstraints) ([]byte, error) {
	if nc == nil {
		return nil, errors.New("nil name constraints")
	}
	permitted, err := marshalSubtrees(nc.permittedSubtrees())
	if err != nil {
		return nil, err
	}
	excluded, err := marshalSubtrees(nc.excludedSubtrees())
	if err != nil {
		return nil, err
	}
	var inner []byte
	if len(permitted) > 0 {
		inner = append(inner, derWrap(derNameConstraintsPermitted, permitted)...)
	}
	if len(excluded) > 0 {
		inner = append(inner, derWrap(derNameConstraintsExcluded, excluded)...)
	}
	return derWrap(derSequence, inner), nil
}

// NameConstraintsExtension returns a pkix.Extension carrying nc.
func NameConstraintsExtension(nc *NameConstraints, critical bool) (pkix.Extension, error) {
	value, err := MarshalNameConstraints(nc)
	if err != nil {
		return pkix.Extension{}, err
	}
	return pkix.Extension{Id: oidExtensionNameConstraints, Critical: critical, Value: value}, nil
}

// SubjectAltNameExtension returns a subjectAltName extension containing
// names, including registeredID values.
func SubjectAltNameExtension(names []GeneralName) (pkix.Extension, error) {
	var inner []byte
	for _, name := range names {
		der, err := marshalGeneralName(name)
		if err != nil {
			return pkix.Extension{}, err
		}
		inner = append(inner, der...)
	}
	return pkix.Extension{Id: oidExtensionSubjectAltName, Value: derWrap(derSequence, inner)}, nil
}

// RegisteredIDName returns a registeredID GeneralName for oid.
func RegisteredIDName(oid asn1.ObjectIdentifier) GeneralName {
	return GeneralName{Type: NameTypeRegisteredID, OID: oid, Value: oid.String()}
}

// DNSName returns a dNSName GeneralName.
func DNSName(name string) GeneralName {
	return GeneralName{Type: NameTypeDNS, Value: name}
}

func marshalSubtrees(subtrees []GeneralSubtree) ([]byte, error) {
	var out []byte
	for _, st := range subtrees {
		base, err := marshalGeneralName(st.Base)
		if err != nil {
			return nil, err
		}
		var body []byte
		body = append(body, base...)
		if st.Minimum != 0 {
			body = append(body, derWrap(derSubtreeMinimum, derInteger(st.Minimum))...)
		}
		if st.HasMax || st.Maximum != 0 {
			body = append(body, derWrap(derSubtreeMaximum, derInteger(st.Maximum))...)
		}
		out = append(out, derWrap(derSequence, body)...)
	}
	return out, nil
}

func marshalGeneralName(name GeneralName) ([]byte, error) {
	switch name.Type {
	case NameTypeEmail:
		if err := checkIA5(name.Value); err != nil {
			return nil, err
		}
		return derWrap(derGeneralNameEmail, []byte(name.Value)), nil
	case NameTypeDNS:
		if err := checkIA5(name.Value); err != nil {
			return nil, err
		}
		return derWrap(derGeneralNameDNS, []byte(name.Value)), nil
	case NameTypeURI:
		if err := checkIA5(name.Value); err != nil {
			return nil, err
		}
		return derWrap(derGeneralNameURI, []byte(name.Value)), nil
	case NameTypeRegisteredID:
		oid := name.OID
		if len(oid) == 0 {
			var err error
			oid, err = ParseOID(name.Value)
			if err != nil {
				return nil, err
			}
		}
		content, err := marshalOIDContent(oid)
		if err != nil {
			return nil, err
		}
		return derWrap(derGeneralNameRegisteredID, content), nil
	case NameTypeIPAddress:
		if name.IP != nil {
			value := []byte(name.IP)
			if name.Mask != nil {
				value = append(value, name.Mask...)
			}
			return derWrap(derGeneralNameIPAddress, value), nil
		}
		return nil, errors.New("iPAddress GeneralName has no IP value")
	default:
		if len(name.Raw) == 0 {
			return nil, errors.New("unsupported GeneralName has no raw value")
		}
		tag := byte(0x80 | uint8(name.Type))
		switch name.Type {
		case NameTypeOtherName:
			tag = derGeneralNameOtherName
		case NameTypeX400Address:
			tag = derGeneralNameX400Address
		case NameTypeDirectoryName:
			tag = derGeneralNameDirectoryName
		case NameTypeEDIPartyName:
			tag = derGeneralNameEDIPartyName
		}
		return derWrap(tag, name.Raw), nil
	}
}

func derWrap(tag byte, content []byte) []byte {
	out := []byte{tag}
	n := len(content)
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 0x100:
		out = append(out, 0x81, byte(n))
	case n < 0x10000:
		out = append(out, 0x82, byte(n>>8), byte(n))
	case n < 0x1000000:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	default:
		out = append(out, 0x84, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, content...)
}

func derInteger(v int) []byte {
	if v < 0 {
		return nil
	}
	if v == 0 {
		return []byte{0}
	}
	var tmp []byte
	for v > 0 {
		tmp = append([]byte{byte(v)}, tmp...)
		v >>= 8
	}
	if tmp[0]&0x80 != 0 {
		tmp = append([]byte{0}, tmp...)
	}
	return tmp
}

func checkIA5(s string) error {
	for _, c := range s {
		if c > 127 {
			return errors.New("value is not an IA5String")
		}
	}
	return nil
}
