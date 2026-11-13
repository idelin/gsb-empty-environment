package netsec

import (
	"errors"
	"fmt"
	"net"
)

// DER tag bytes used by GeneralName and NameConstraints. GeneralName tags
// are context-specific and either primitive (0x8n) or constructed (0xAn).
const (
	derSequence = 0x30

	derGeneralNameOtherName     = 0xA0
	derGeneralNameEmail         = 0x81
	derGeneralNameDNS           = 0x82
	derGeneralNameX400Address   = 0xA3
	derGeneralNameDirectoryName = 0xA4
	derGeneralNameEDIPartyName  = 0xA5
	derGeneralNameURI           = 0x86
	derGeneralNameIPAddress     = 0x87
	derGeneralNameRegisteredID  = 0x88

	derNameConstraintsPermitted = 0xA0
	derNameConstraintsExcluded  = 0xA1
	derSubtreeMinimum           = 0x80
	derSubtreeMaximum           = 0x81
)

var errInvalidDER = errors.New("invalid DER")

// readDERValue splits one definite-length DER value into its tag, content,
// and the remaining bytes. GeneralName and NameConstraints only use
// single-byte tag numbers, so high-tag-number form is rejected.
func readDERValue(b []byte) (tag byte, value, rest []byte, err error) {
	if len(b) == 0 {
		return 0, nil, nil, errInvalidDER
	}
	tag = b[0]
	if tag&0x1f == 0x1f {
		return 0, nil, nil, fmt.Errorf("%w: high tag number", errInvalidDER)
	}
	if len(b) < 2 {
		return 0, nil, nil, fmt.Errorf("%w: truncated length", errInvalidDER)
	}
	length := int(b[1])
	offset := 2
	if b[1]&0x80 != 0 {
		n := int(b[1] & 0x7f)
		if n == 0 {
			return 0, nil, nil, fmt.Errorf("%w: indefinite length", errInvalidDER)
		}
		if n > 4 {
			return 0, nil, nil, fmt.Errorf("%w: length uses %d octets", errInvalidDER, n)
		}
		if len(b) < 2+n {
			return 0, nil, nil, fmt.Errorf("%w: truncated long length", errInvalidDER)
		}
		length = 0
		for i := 0; i < n; i++ {
			if length > (int(^uint(0)>>1) >> 8) {
				return 0, nil, nil, fmt.Errorf("%w: length overflow", errInvalidDER)
			}
			length = length<<8 | int(b[2+i])
		}
		offset += n
	}
	if length < 0 || len(b) < offset+length {
		return 0, nil, nil, fmt.Errorf("%w: truncated value", errInvalidDER)
	}
	return tag, b[offset : offset+length], b[offset+length:], nil
}

func parseGeneralName(tag byte, value []byte) (GeneralName, error) {
	name := GeneralName{Raw: append([]byte(nil), value...)}
	switch tag {
	case derGeneralNameOtherName:
		name.Type = NameTypeOtherName
		name.Value = string(value)
	case derGeneralNameEmail:
		name.Type = NameTypeEmail
		name.Value = string(value)
		if err := checkIA5(name.Value); err != nil {
			return GeneralName{}, fmt.Errorf("rfc822Name: %w", err)
		}
	case derGeneralNameDNS:
		name.Type = NameTypeDNS
		name.Value = string(value)
		if err := checkIA5(name.Value); err != nil {
			return GeneralName{}, fmt.Errorf("dNSName: %w", err)
		}
	case derGeneralNameX400Address:
		name.Type = NameTypeX400Address
		name.Value = string(value)
	case derGeneralNameDirectoryName:
		name.Type = NameTypeDirectoryName
		name.Value = string(value)
	case derGeneralNameEDIPartyName:
		name.Type = NameTypeEDIPartyName
		name.Value = string(value)
	case derGeneralNameURI:
		name.Type = NameTypeURI
		name.Value = string(value)
		if err := checkIA5(name.Value); err != nil {
			return GeneralName{}, fmt.Errorf("uniformResourceIdentifier: %w", err)
		}
	case derGeneralNameIPAddress:
		name.Type = NameTypeIPAddress
		name.Value = string(value)
		switch len(value) {
		case 4, 16:
			name.IP = append(net.IP(nil), value...)
			name.Value = name.IP.String()
		case 8, 32:
			name.IP = append(net.IP(nil), value[:len(value)/2]...)
			name.Mask = append(net.IPMask(nil), value[len(value)/2:]...)
			name.Value = name.IP.String() + "/" + name.Mask.String()
		}
	case derGeneralNameRegisteredID:
		name.Type = NameTypeRegisteredID
		oid, err := decodeOIDContent(value)
		if err != nil {
			return GeneralName{}, fmt.Errorf("registeredID: %w", err)
		}
		name.OID = oid
		name.Value = oid.String()
	default:
		return GeneralName{}, fmt.Errorf("%w: unsupported GeneralName tag 0x%02x", errInvalidDER, tag)
	}
	return name, nil
}
