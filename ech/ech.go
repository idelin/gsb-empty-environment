// Package ech validates Encrypted Client Hello extensions found in a TLS
// ClientHello.
package ech

import "encoding/binary"

const ExtensionType uint16 = 0xfe0d

const x25519EncapsulationKeyLength = 32

type ClientHelloType uint8

const (
	Outer ClientHelloType = 0
	Inner ClientHelloType = 1
)

type Alert uint8

const (
	AlertIllegalParameter Alert = 47
	AlertDecodeError      Alert = 50
)

func (a Alert) Error() string {
	switch a {
	case AlertIllegalParameter:
		return "illegal_parameter"
	case AlertDecodeError:
		return "decode_error"
	default:
		return "unknown_tls_alert"
	}
}

type KEM struct {
	ID                     uint16
	EncapsulationKeyLength int
}

var X25519KEM = KEM{ID: 0x0020, EncapsulationKeyLength: x25519EncapsulationKeyLength}

type Config struct {
	ID  uint8
	KEM KEM
}

type Options struct {
	Configs                []Config
	RetryConfigs           []byte
	AfterHelloRetryRequest bool
	InnerClientHello       bool
}

type Extension struct {
	Type     ClientHelloType
	KDF      uint16
	AEAD     uint16
	ConfigID uint8
	Enc      []byte
	Payload  []byte
}

type Result struct {
	Extension        *Extension
	UsedECH          bool
	SendRetryConfigs bool
	RetryConfigs     []byte
}

// ValidateClientHelloExtensions validates the ClientHello extensions vector.
// The vector uses the TLS wire format: a two-byte length followed by extension
// entries consisting of a two-byte type and a two-byte data length.
func ValidateClientHelloExtensions(extensionList []byte, options Options) (*Result, error) {
	if len(extensionList) < 2 {
		return nil, AlertDecodeError
	}

	declaredLength := int(binary.BigEndian.Uint16(extensionList))
	if declaredLength != len(extensionList)-2 {
		return nil, AlertDecodeError
	}

	rest := extensionList[2:]
	sawECH := false
	var echData []byte

	for len(rest) > 0 {
		if len(rest) < 4 {
			return nil, AlertDecodeError
		}

		extensionType := binary.BigEndian.Uint16(rest)
		dataLength := int(binary.BigEndian.Uint16(rest[2:]))
		if dataLength > len(rest)-4 {
			return nil, AlertDecodeError
		}

		if extensionType == ExtensionType {
			if sawECH {
				return nil, AlertIllegalParameter
			}
			sawECH = true
			echData = rest[4 : 4+dataLength]
		}

		rest = rest[4+dataLength:]
	}

	if !sawECH {
		return &Result{}, nil
	}

	return ValidateExtension(echData, options)
}

// ValidateExtension validates the data carried by an ECH extension.
func ValidateExtension(data []byte, options Options) (*Result, error) {
	if len(data) == 0 {
		return nil, AlertDecodeError
	}

	switch ClientHelloType(data[0]) {
	case Inner:
		if !options.InnerClientHello {
			return nil, AlertIllegalParameter
		}
		if len(data) != 1 {
			return nil, AlertDecodeError
		}
		return &Result{Extension: &Extension{Type: Inner}}, nil

	case Outer:
		if options.InnerClientHello {
			return nil, AlertIllegalParameter
		}
		return validateOuter(data, options)

	default:
		return nil, AlertDecodeError
	}
}

func validateOuter(data []byte, options Options) (*Result, error) {
	const fixedFieldsLength = 8

	if len(data) < fixedFieldsLength {
		return nil, AlertDecodeError
	}

	extension := &Extension{
		Type:     Outer,
		KDF:      binary.BigEndian.Uint16(data[1:]),
		AEAD:     binary.BigEndian.Uint16(data[3:]),
		ConfigID: data[5],
	}

	encLength := int(binary.BigEndian.Uint16(data[6:]))
	encEnd := fixedFieldsLength + encLength
	if encEnd < fixedFieldsLength || encEnd+2 > len(data) {
		return nil, AlertDecodeError
	}

	payloadLength := int(binary.BigEndian.Uint16(data[encEnd:]))
	payloadEnd := encEnd + 2 + payloadLength
	if payloadEnd < encEnd+2 || payloadEnd != len(data) || payloadLength == 0 {
		return nil, AlertDecodeError
	}

	extension.Enc = data[fixedFieldsLength:encEnd]
	extension.Payload = data[encEnd+2 : payloadEnd]

	config, configKnown := findConfig(options.Configs, extension.ConfigID)
	if options.AfterHelloRetryRequest {
		if encLength != 0 {
			return nil, AlertDecodeError
		}
	} else {
		expectedLength := x25519EncapsulationKeyLength
		if configKnown {
			expectedLength = config.KEM.EncapsulationKeyLength
		}
		if encLength != expectedLength {
			return nil, AlertDecodeError
		}
	}

	result := &Result{
		Extension: extension,
		UsedECH:   configKnown,
	}
	if !configKnown {
		result.SendRetryConfigs = true
		result.RetryConfigs = options.RetryConfigs
	}

	return result, nil
}

func findConfig(configs []Config, id uint8) (Config, bool) {
	for _, config := range configs {
		if config.ID == id {
			return config, true
		}
	}
	return Config{}, false
}
