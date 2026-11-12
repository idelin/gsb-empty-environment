package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	ErrMalformedOuterExtensions      = errors.New("malformed ech_outer_extensions")
	ErrECHOuterExtensionsInOuter     = errors.New("ech_outer_extensions in ClientHelloOuter")
	ErrDuplicateExtension            = errors.New("duplicate extension type")
	ErrMissingReferencedExtension    = errors.New("referenced extension missing from ClientHelloOuter")
	ErrDuplicateReferencedExtension  = errors.New("extension referenced more than once")
	ErrForbiddenECHReference         = errors.New("reference to encrypted_client_hello")
	ErrForbiddenSelfReference        = errors.New("ech_outer_extensions references itself")
	ErrForbiddenServerNameReference  = errors.New("reference to server_name would use outer SNI")
	ErrOutOfOrderReferencedExtension = errors.New("referenced extensions are not in ClientHelloOuter order")
	ErrMissingECHInner               = errors.New("ClientHelloInner has no encrypted_client_hello extension")
	ErrMalformedECHInner             = errors.New("ClientHelloInner encrypted_client_hello extension is not type inner")
	ErrExpandedClientHelloTooLarge   = errors.New("expanded ClientHello extensions are too large")
)

type Alert uint8

const (
	AlertNone             Alert = 0
	AlertIllegalParameter Alert = 47
	AlertDecryptError     Alert = 51
)

type ProtocolError struct {
	Alert  Alert
	Reason error
}

func (e *ProtocolError) Error() string {
	if e == nil || e.Reason == nil {
		return "TLS protocol error"
	}
	return e.Reason.Error()
}

func (e *ProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Reason
}

func AlertForError(err error) Alert {
	if err == nil {
		return AlertNone
	}
	var protocolErr *ProtocolError
	if errors.As(err, &protocolErr) {
		return protocolErr.Alert
	}
	return AlertIllegalParameter
}

func illegalParameter(err error) error {
	if err == nil {
		return nil
	}
	var protocolErr *ProtocolError
	if errors.As(err, &protocolErr) {
		return err
	}
	return &ProtocolError{Alert: AlertIllegalParameter, Reason: err}
}

func ReconstructClientHelloInner(outerRaw, encodedInner []byte) (*ClientHello, error) {
	outer, err := ParseClientHello(outerRaw)
	if err != nil {
		return nil, illegalParameter(err)
	}
	return reconstructClientHelloInner(outer, encodedInner)
}

func ReconstructClientHelloInnerFromClientHello(outer *ClientHello, encodedInner []byte) (*ClientHello, error) {
	return reconstructClientHelloInner(outer, encodedInner)
}

func reconstructClientHelloInner(outer *ClientHello, encodedInner []byte) (*ClientHello, error) {
	if outer == nil {
		return nil, illegalParameter(fmt.Errorf("%w: nil ClientHelloOuter", ErrMalformedClientHello))
	}
	inner, err := parseEncodedClientHelloInner(encodedInner)
	if err != nil {
		return nil, illegalParameter(err)
	}
	if err := validateInnerExtensionTypes(inner.Extensions); err != nil {
		return nil, illegalParameter(err)
	}
	if err := validateECHInnerExtension(inner); err != nil {
		return nil, illegalParameter(err)
	}
	if outer.ExtensionCount(ExtensionECHOuterExtensions) != 0 {
		return nil, illegalParameter(ErrECHOuterExtensionsInOuter)
	}
	if outer.ExtensionCount(ExtensionEncryptedClientHello) > 1 {
		return nil, illegalParameter(fmt.Errorf("%w: encrypted_client_hello", ErrDuplicateExtension))
	}

	expanded, err := expandOuterExtensions(inner, outer)
	if err != nil {
		return nil, illegalParameter(err)
	}
	inner.Extensions = expanded
	inner.SessionID = append([]byte(nil), outer.SessionID...)

	if err := validateInnerExtensionTypes(inner.Extensions); err != nil {
		return nil, illegalParameter(err)
	}
	if err := validateECHInnerExtension(inner); err != nil {
		return nil, illegalParameter(err)
	}
	return inner, nil
}

func parseEncodedClientHelloInner(raw []byte) (*ClientHello, error) {
	body, err := clientHelloBody(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedEncodedInner, err)
	}
	inner, consumed, err := parseClientHelloPrefix(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedEncodedInner, err)
	}
	for _, b := range body[consumed:] {
		if b != 0 {
			return nil, ErrNonZeroInnerPadding
		}
	}
	return inner, nil
}

func validateECHInnerExtension(inner *ClientHello) error {
	count := inner.ExtensionCount(ExtensionEncryptedClientHello)
	if count == 0 {
		return ErrMissingECHInner
	}
	if count != 1 {
		return fmt.Errorf("%w: encrypted_client_hello", ErrDuplicateExtension)
	}
	data, _ := inner.Extension(ExtensionEncryptedClientHello)
	if len(data) != 1 || data[0] != ECHTypeInner {
		return ErrMalformedECHInner
	}
	return nil
}

func validateInnerExtensionTypes(extensions []Extension) error {
	seen := make(map[uint16]struct{}, len(extensions))
	for _, ext := range extensions {
		if _, ok := seen[ext.Type]; ok {
			return fmt.Errorf("%w: 0x%04x", ErrDuplicateExtension, ext.Type)
		}
		seen[ext.Type] = struct{}{}
	}
	return nil
}

func expandOuterExtensions(inner, outer *ClientHello) ([]Extension, error) {
	outerExtensionsIndex := -1
	for i, ext := range inner.Extensions {
		if ext.Type == ExtensionECHOuterExtensions {
			outerExtensionsIndex = i
			break
		}
	}
	if outerExtensionsIndex == -1 {
		return cloneExtensions(inner.Extensions), nil
	}

	referencedTypes, err := parseOuterExtensionTypes(inner.Extensions[outerExtensionsIndex].Data)
	if err != nil {
		return nil, err
	}

	outerPositions := make(map[uint16][]int, len(outer.Extensions))
	for i, ext := range outer.Extensions {
		outerPositions[ext.Type] = append(outerPositions[ext.Type], i)
	}
	innerTypes := make(map[uint16]struct{}, len(inner.Extensions))
	for _, ext := range inner.Extensions {
		innerTypes[ext.Type] = struct{}{}
	}

	referenced := make([]Extension, 0, len(referencedTypes))
	seenReferences := make(map[uint16]struct{}, len(referencedTypes))
	lastOuterPosition := -1
	for _, typ := range referencedTypes {
		switch typ {
		case ExtensionEncryptedClientHello:
			return nil, ErrForbiddenECHReference
		case ExtensionECHOuterExtensions:
			return nil, ErrForbiddenSelfReference
		case ExtensionServerName:
			return nil, ErrForbiddenServerNameReference
		}
		if _, ok := seenReferences[typ]; ok {
			return nil, fmt.Errorf("%w: 0x%04x", ErrDuplicateReferencedExtension, typ)
		}
		seenReferences[typ] = struct{}{}

		positions, ok := outerPositions[typ]
		if !ok || len(positions) == 0 {
			return nil, fmt.Errorf("%w: 0x%04x", ErrMissingReferencedExtension, typ)
		}
		if len(positions) != 1 {
			return nil, fmt.Errorf("%w: 0x%04x occurs %d times", ErrDuplicateExtension, typ, len(positions))
		}
		if positions[0] <= lastOuterPosition {
			return nil, fmt.Errorf("%w: 0x%04x", ErrOutOfOrderReferencedExtension, typ)
		}
		lastOuterPosition = positions[0]

		if _, ok := innerTypes[typ]; ok {
			return nil, fmt.Errorf("%w: 0x%04x appears in inner and OuterExtensions", ErrDuplicateExtension, typ)
		}
		referenced = append(referenced, Extension{
			Type: typ,
			Data: append([]byte(nil), outer.Extensions[positions[0]].Data...),
		})
	}

	result := make([]Extension, 0, len(inner.Extensions)-1+len(referenced))
	for i, ext := range inner.Extensions {
		if i == outerExtensionsIndex {
			result = append(result, referenced...)
			continue
		}
		result = append(result, Extension{
			Type: ext.Type,
			Data: append([]byte(nil), ext.Data...),
		})
	}
	if err := validateInnerExtensionTypes(result); err != nil {
		return nil, err
	}
	if extensionBlockLength(result) > 0xffff {
		return nil, ErrExpandedClientHelloTooLarge
	}
	return result, nil
}

func parseOuterExtensionTypes(data []byte) ([]uint16, error) {
	if len(data) < 2 || len(data) > 254 || len(data)%2 != 0 {
		return nil, fmt.Errorf("%w: invalid list length %d", ErrMalformedOuterExtensions, len(data))
	}
	types := make([]uint16, 0, len(data)/2)
	for pos := 0; pos < len(data); pos += 2 {
		types = append(types, binary.BigEndian.Uint16(data[pos:pos+2]))
	}
	return types, nil
}

func extensionBlockLength(extensions []Extension) int {
	length := 0
	for _, ext := range extensions {
		length += 4 + len(ext.Data)
	}
	return length
}
