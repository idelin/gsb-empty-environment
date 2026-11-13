package netsec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
)

const (
	// HandshakeClientHello is the TLS handshake message type for ClientHello.
	HandshakeClientHello = 1

	// ExtensionSupportedGroups is the TLS extension type for supported_groups
	// (formerly elliptic_curves).
	ExtensionSupportedGroups = 10

	// ExtensionKeyShare is the TLS 1.3 key_share extension type.
	ExtensionKeyShare = 51
)

// NamedGroup is a TLS named group identifier. It is an alias so callers can
// use either uint16 or NamedGroup values interchangeably.
type NamedGroup = uint16

var (
	// ErrMalformedClientHello reports an input that is not a well-formed TLS
	// ClientHello.
	ErrMalformedClientHello = errors.New("netsec: malformed ClientHello")

	// ErrDuplicateGroup reports a group that occurs more than once in a list
	// that requires unique groups.
	ErrDuplicateGroup = errors.New("netsec: duplicate group")

	// ErrDuplicateSupportedGroup reports a duplicate in supported_groups.
	ErrDuplicateSupportedGroup = errors.New("netsec: duplicate group in supported_groups")

	// ErrDuplicateKeyShareGroup reports a duplicate in key_share.
	ErrDuplicateKeyShareGroup = errors.New("netsec: duplicate group in key_share")

	// ErrDuplicateExtension reports an extension that occurs more than once.
	ErrDuplicateExtension = errors.New("netsec: duplicate extension")

	// Aliases retained for callers that name the lists in the plural.
	ErrDuplicateSupportedGroups = ErrDuplicateSupportedGroup
	ErrDuplicateKeyShares       = ErrDuplicateKeyShareGroup
)

// DuplicateGroupError identifies the list and group responsible for a
// duplicate-group rejection.
type DuplicateGroupError struct {
	List  string
	Group NamedGroup
}

func (e *DuplicateGroupError) Error() string {
	return fmt.Sprintf("netsec: duplicate group 0x%04x in %s", e.Group, e.List)
}

// Is lets callers match either the generic duplicate error or the error for a
// particular TLS list with errors.Is.
func (e *DuplicateGroupError) Is(target error) bool {
	if target == ErrDuplicateGroup {
		return true
	}
	switch e.List {
	case "supported_groups":
		return target == ErrDuplicateSupportedGroup
	case "key_share":
		return target == ErrDuplicateKeyShareGroup
	default:
		return false
	}
}

// Extension is a TLS extension in a ClientHello.
type Extension struct {
	Type uint16
	Data []byte
}

// KeyShareEntry is one entry in a TLS 1.3 key_share extension.
type KeyShareEntry struct {
	Group       NamedGroup
	KeyExchange []byte
	// Data is an alias for KeyExchange.
	Data []byte
}

// KeyShare is an alias retained for callers that prefer the TLS extension's
// entry name without the "Entry" suffix.
type KeyShare = KeyShareEntry

// ClientHello is the decoded form of a TLS ClientHello.
//
// SupportedGroups and KeyShares contain the values from the corresponding
// extensions, in wire order. A successful decode guarantees that neither list
// contains a repeated group.
type ClientHello struct {
	LegacyVersion      uint16
	Random             [32]byte
	SessionID          []byte
	SessionId          []byte
	CipherSuites       []uint16
	CompressionMethods []byte
	Extensions         []Extension

	SupportedGroups []NamedGroup
	SupportedCurves []NamedGroup
	KeyShares       []KeyShareEntry
	KeyShare        []KeyShareEntry

	// Version is an alias for LegacyVersion, useful to callers that model the
	// ClientHello version directly.
	Version uint16
}

// ParseClientHello decodes a TLS ClientHello. It accepts a complete TLS
// record (or multiple records containing the handshake), a handshake message
// including its four-byte header, or the bare ClientHello body. It also accepts
// an io.Reader or a string containing any of those forms.
func ParseClientHello(input any) (*ClientHello, error) {
	raw, err := inputBytes(input)
	if err != nil {
		return nil, err
	}

	body, err := clientHelloBody(raw)
	if err != nil {
		return nil, err
	}
	return parseClientHelloBody(body)
}

// DecodeClientHello is an alias for ParseClientHello.
func DecodeClientHello(input any) (*ClientHello, error) {
	return ParseClientHello(input)
}

// UnmarshalClientHello is an alias for ParseClientHello.
func UnmarshalClientHello(input any) (*ClientHello, error) {
	return ParseClientHello(input)
}

// DecodeClientHelloFromBytes is an alias for ParseClientHello with a
// byte-slice signature.
func DecodeClientHelloFromBytes(data []byte) (*ClientHello, error) {
	return ParseClientHello(data)
}

// Parse is an alias for ParseClientHello.
func Parse(input any) (*ClientHello, error) {
	return ParseClientHello(input)
}

// Decode is an alias for ParseClientHello.
func Decode(input any) (*ClientHello, error) {
	return ParseClientHello(input)
}

// Unmarshal is an alias for ParseClientHello.
func Unmarshal(input any) (*ClientHello, error) {
	return ParseClientHello(input)
}

// ValidateClientHello decodes a ClientHello and reports any duplicate groups
// or structural errors.
func ValidateClientHello(input any) error {
	_, err := ParseClientHello(input)
	return err
}

// Unmarshal decodes data into c, replacing any previous contents. It rejects
// duplicate groups in supported_groups and key_share.
func (c *ClientHello) Unmarshal(input any) error {
	if c == nil {
		return fmt.Errorf("%w: nil ClientHello receiver", ErrMalformedClientHello)
	}
	parsed, err := ParseClientHello(input)
	if err != nil {
		return err
	}
	*c = *parsed
	return nil
}

// Decode decodes data into c, replacing any previous contents.
func (c *ClientHello) Decode(input any) error {
	return c.Unmarshal(input)
}

func inputBytes(input any) ([]byte, error) {
	switch v := input.(type) {
	case nil:
		return nil, fmt.Errorf("%w: nil input", ErrMalformedClientHello)
	case []byte:
		return v, nil
	case string:
		if decoded, ok := decodeHexString(v); ok {
			return decoded, nil
		}
		return []byte(v), nil
	case io.Reader:
		data, err := io.ReadAll(v)
		if err != nil {
			return nil, fmt.Errorf("%w: reading input: %v", ErrMalformedClientHello, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("%w: unsupported input type %T", ErrMalformedClientHello, input)
	}
}

func decodeHexString(value string) ([]byte, bool) {
	var compact []byte
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '\t', '\r', '\n', ':', '-', ',':
			continue
		}
		compact = append(compact, value[i])
	}
	if len(compact) >= 2 && compact[0] == '0' && (compact[1] == 'x' || compact[1] == 'X') {
		compact = compact[2:]
	}
	if len(compact) == 0 || len(compact)%2 != 0 {
		return nil, false
	}
	for _, b := range compact {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return nil, false
		}
	}
	decoded, err := hex.DecodeString(string(compact))
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func clientHelloBody(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedClientHello)
	}

	// A complete TLS record begins with the handshake content type. Records
	// may split the ClientHello, so concatenate their payloads.
	if data[0] == 22 {
		var handshake []byte
		offset := 0
		for offset < len(data) {
			if len(data)-offset < 5 {
				return nil, fmt.Errorf("%w: truncated TLS record header", ErrMalformedClientHello)
			}
			contentType := data[offset]
			recordLen := int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
			offset += 5
			if recordLen > len(data)-offset {
				return nil, fmt.Errorf("%w: truncated TLS record", ErrMalformedClientHello)
			}
			if contentType != 22 {
				return nil, fmt.Errorf("%w: unexpected TLS record type %d", ErrMalformedClientHello, contentType)
			}
			handshake = append(handshake, data[offset:offset+recordLen]...)
			offset += recordLen
		}
		data = handshake
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty handshake", ErrMalformedClientHello)
	}

	// Strip the handshake header when present. The first byte of a bare
	// ClientHello body is the high byte of legacy_version (normally 0x03), so
	// a leading 0x01 is unambiguous.
	if data[0] == HandshakeClientHello {
		if len(data) < 4 {
			return nil, fmt.Errorf("%w: truncated handshake header", ErrMalformedClientHello)
		}
		length := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		if length < 34 {
			return nil, fmt.Errorf("%w: handshake too short", ErrMalformedClientHello)
		}
		if length > len(data)-4 {
			return nil, fmt.Errorf("%w: truncated handshake body", ErrMalformedClientHello)
		}
		data = data[4 : 4+length]
	}

	if len(data) < 34 {
		return nil, fmt.Errorf("%w: body too short", ErrMalformedClientHello)
	}
	return data, nil
}

func parseClientHelloBody(data []byte) (*ClientHello, error) {
	ch := &ClientHello{}
	offset := 0

	ch.LegacyVersion = binary.BigEndian.Uint16(data[offset : offset+2])
	ch.Version = ch.LegacyVersion
	offset += 2

	copy(ch.Random[:], data[offset:offset+32])
	offset += 32

	sessionID, next, err := takeVector8(data, offset, "session_id")
	if err != nil {
		return nil, err
	}
	if len(sessionID) > 32 {
		return nil, fmt.Errorf("%w: session_id is longer than 32 bytes", ErrMalformedClientHello)
	}
	ch.SessionID = append([]byte(nil), sessionID...)
	ch.SessionId = ch.SessionID
	offset = next

	cipherSuites, next, err := takeVector16(data, offset, "cipher_suites")
	if err != nil {
		return nil, err
	}
	if len(cipherSuites) == 0 || len(cipherSuites)%2 != 0 {
		return nil, fmt.Errorf("%w: invalid cipher_suites length", ErrMalformedClientHello)
	}
	ch.CipherSuites = make([]uint16, 0, len(cipherSuites)/2)
	for i := 0; i < len(cipherSuites); i += 2 {
		ch.CipherSuites = append(ch.CipherSuites, binary.BigEndian.Uint16(cipherSuites[i:i+2]))
	}
	offset = next

	compressionMethods, next, err := takeVector8(data, offset, "compression_methods")
	if err != nil {
		return nil, err
	}
	if len(compressionMethods) == 0 {
		return nil, fmt.Errorf("%w: empty compression_methods", ErrMalformedClientHello)
	}
	ch.CompressionMethods = append([]byte(nil), compressionMethods...)
	offset = next

	if offset == len(data) {
		return ch, nil
	}
	if len(data)-offset < 2 {
		return nil, fmt.Errorf("%w: truncated extensions length", ErrMalformedClientHello)
	}
	extensionsLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	if extensionsLen != len(data)-offset {
		return nil, fmt.Errorf("%w: extensions length mismatch", ErrMalformedClientHello)
	}

	seenExtensions := make(map[uint16]struct{})
	for offset < len(data) {
		if len(data)-offset < 4 {
			return nil, fmt.Errorf("%w: truncated extension header", ErrMalformedClientHello)
		}
		extType := binary.BigEndian.Uint16(data[offset : offset+2])
		extLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		if extLen > len(data)-offset {
			return nil, fmt.Errorf("%w: truncated extension 0x%04x", ErrMalformedClientHello, extType)
		}
		extData := data[offset : offset+extLen]
		offset += extLen

		if _, ok := seenExtensions[extType]; ok {
			return nil, fmt.Errorf("%w: extension 0x%04x", ErrDuplicateExtension, extType)
		}
		seenExtensions[extType] = struct{}{}
		ch.Extensions = append(ch.Extensions, Extension{
			Type: extType,
			Data: append([]byte(nil), extData...),
		})

		switch extType {
		case ExtensionSupportedGroups:
			groups, err := parseSupportedGroupsData(extData)
			if err != nil {
				return nil, err
			}
			ch.SupportedGroups = groups
			ch.SupportedCurves = groups
		case ExtensionKeyShare:
			shares, err := parseKeyShareData(extData)
			if err != nil {
				return nil, err
			}
			ch.KeyShares = shares
			ch.KeyShare = shares
		}
	}

	return ch, nil
}

func takeVector8(data []byte, offset int, name string) ([]byte, int, error) {
	if offset >= len(data) {
		return nil, 0, fmt.Errorf("%w: missing %s length", ErrMalformedClientHello, name)
	}
	length := int(data[offset])
	offset++
	if length > len(data)-offset {
		return nil, 0, fmt.Errorf("%w: truncated %s", ErrMalformedClientHello, name)
	}
	return data[offset : offset+length], offset + length, nil
}

func takeVector16(data []byte, offset int, name string) ([]byte, int, error) {
	if len(data)-offset < 2 {
		return nil, 0, fmt.Errorf("%w: missing %s length", ErrMalformedClientHello, name)
	}
	length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	if length > len(data)-offset {
		return nil, 0, fmt.Errorf("%w: truncated %s", ErrMalformedClientHello, name)
	}
	return data[offset : offset+length], offset + length, nil
}

func parseSupportedGroupsData(data []byte) ([]NamedGroup, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: supported_groups is too short", ErrMalformedClientHello)
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if listLen != len(data)-2 {
		return nil, fmt.Errorf("%w: supported_groups length mismatch", ErrMalformedClientHello)
	}
	if listLen%2 != 0 {
		return nil, fmt.Errorf("%w: supported_groups has an odd length", ErrMalformedClientHello)
	}

	groups := make([]NamedGroup, 0, listLen/2)
	seen := make(map[NamedGroup]struct{}, listLen/2)
	for offset := 2; offset < len(data); offset += 2 {
		group := binary.BigEndian.Uint16(data[offset : offset+2])
		if _, ok := seen[group]; ok {
			return nil, &DuplicateGroupError{List: "supported_groups", Group: group}
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups, nil
}

func parseKeyShareData(data []byte) ([]KeyShareEntry, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: key_share is too short", ErrMalformedClientHello)
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if listLen != len(data)-2 {
		return nil, fmt.Errorf("%w: key_share length mismatch", ErrMalformedClientHello)
	}

	shares := make([]KeyShareEntry, 0, 2)
	seen := make(map[NamedGroup]struct{})
	offset := 2
	for offset < len(data) {
		if len(data)-offset < 4 {
			return nil, fmt.Errorf("%w: truncated key_share entry", ErrMalformedClientHello)
		}
		group := binary.BigEndian.Uint16(data[offset : offset+2])
		keyLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		if keyLen > len(data)-offset {
			return nil, fmt.Errorf("%w: truncated key_share key exchange", ErrMalformedClientHello)
		}
		if _, ok := seen[group]; ok {
			return nil, &DuplicateGroupError{List: "key_share", Group: group}
		}
		seen[group] = struct{}{}
		shares = append(shares, KeyShareEntry{
			Group:       group,
			KeyExchange: append([]byte(nil), data[offset:offset+keyLen]...),
			Data:        append([]byte(nil), data[offset:offset+keyLen]...),
		})
		offset += keyLen
	}
	return shares, nil
}

// ParseSupportedGroups decodes the body of a supported_groups extension. It
// accepts the extension body (a two-byte list length followed by groups), the
// complete extension (type and length included), or a bare list of group
// identifiers.
func ParseSupportedGroups(data []byte) ([]NamedGroup, error) {
	data = stripExtensionHeader(data, ExtensionSupportedGroups)

	if len(data) >= 2 {
		listLen := int(binary.BigEndian.Uint16(data[:2]))
		if listLen == len(data)-2 && listLen%2 == 0 {
			return parseSupportedGroupsData(data)
		}
	}
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("%w: supported_groups has an odd length", ErrMalformedClientHello)
	}
	body := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(body[:2], uint16(len(data)))
	copy(body[2:], data)
	return parseSupportedGroupsData(body)
}

// DecodeSupportedGroups is an alias for ParseSupportedGroups.
func DecodeSupportedGroups(data []byte) ([]NamedGroup, error) {
	return ParseSupportedGroups(data)
}

// UnmarshalSupportedGroups is an alias for ParseSupportedGroups.
func UnmarshalSupportedGroups(data []byte) ([]NamedGroup, error) {
	return ParseSupportedGroups(data)
}

// ParseKeyShare decodes the body of a key_share extension. It accepts the
// extension body (a two-byte client_shares length followed by entries), the
// complete extension, or bare key share entries.
func ParseKeyShare(data []byte) ([]KeyShareEntry, error) {
	data = stripExtensionHeader(data, ExtensionKeyShare)

	if len(data) >= 2 {
		listLen := int(binary.BigEndian.Uint16(data[:2]))
		if listLen == len(data)-2 {
			return parseKeyShareData(data)
		}
	}
	body := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(body[:2], uint16(len(data)))
	copy(body[2:], data)
	return parseKeyShareData(body)
}

// DecodeKeyShare is an alias for ParseKeyShare.
func DecodeKeyShare(data []byte) ([]KeyShareEntry, error) {
	return ParseKeyShare(data)
}

// UnmarshalKeyShare is an alias for ParseKeyShare.
func UnmarshalKeyShare(data []byte) ([]KeyShareEntry, error) {
	return ParseKeyShare(data)
}

func stripExtensionHeader(data []byte, wantType uint16) []byte {
	if len(data) >= 4 &&
		binary.BigEndian.Uint16(data[:2]) == wantType &&
		int(binary.BigEndian.Uint16(data[2:4])) == len(data)-4 {
		return data[4:]
	}
	return data
}

// ValidateSupportedGroups reports whether the supported_groups list contains
// any repeated group. It accepts []uint16, []NamedGroup, the raw extension
// bytes, or a slice of key-share-like structs with a Group field.
func ValidateSupportedGroups(value any) error {
	groups, err := groupsFrom(value)
	if err != nil {
		return err
	}
	seen := make(map[NamedGroup]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			return &DuplicateGroupError{List: "supported_groups", Group: group}
		}
		seen[group] = struct{}{}
	}
	return nil
}

// ValidateKeyShares reports whether the key_share list contains any repeated
// group. It accepts the same input forms as ValidateSupportedGroups.
func ValidateKeyShares(value any) error {
	return ValidateKeyShareGroups(value)
}

// ValidateKeyShareGroups reports whether a list of key_share group identifiers
// contains any repeated group. It accepts the same input forms as
// ValidateSupportedGroups.
func ValidateKeyShareGroups(value any) error {
	if raw, ok := value.([]byte); ok {
		body := stripExtensionHeader(raw, ExtensionKeyShare)
		if len(body) >= 2 && int(binary.BigEndian.Uint16(body[:2])) == len(body)-2 {
			shares, err := parseKeyShareData(body)
			if err != nil {
				return err
			}
			value = shares
		}
	}
	groups, err := groupsFrom(value)
	if err != nil {
		return err
	}
	seen := make(map[NamedGroup]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			return &DuplicateGroupError{List: "key_share", Group: group}
		}
		seen[group] = struct{}{}
	}
	return nil
}

// ValidateKeyShare is an alias for ValidateKeyShareGroups.
func ValidateKeyShare(value any) error {
	return ValidateKeyShareGroups(value)
}

// ValidateGroups checks any supported group list or key share list supplied as
// a slice, a ClientHello, or a pointer to one. It is a convenience entry point
// for callers that do not want to distinguish the two list representations.
func ValidateGroups(value any) error {
	if value == nil {
		return nil
	}

	if ch, ok := value.(*ClientHello); ok {
		if ch == nil {
			return nil
		}
		if err := ValidateSupportedGroups(effectiveSupportedGroups(ch)); err != nil {
			return err
		}
		return ValidateKeyShares(effectiveKeyShares(ch))
	}
	if ch, ok := value.(ClientHello); ok {
		if err := ValidateSupportedGroups(effectiveSupportedGroups(&ch)); err != nil {
			return err
		}
		return ValidateKeyShares(effectiveKeyShares(&ch))
	}
	if raw, ok := value.([]byte); ok {
		keyBody := stripExtensionHeader(raw, ExtensionKeyShare)
		if len(keyBody) >= 2 && int(binary.BigEndian.Uint16(keyBody[:2])) == len(keyBody)-2 {
			shares, err := parseKeyShareData(keyBody)
			if err == nil {
				return ValidateKeyShares(shares)
			}
			if errors.Is(err, ErrDuplicateGroup) {
				return err
			}
		}
		groupBody := stripExtensionHeader(raw, ExtensionSupportedGroups)
		if len(groupBody) >= 2 && int(binary.BigEndian.Uint16(groupBody[:2])) == len(groupBody)-2 {
			groups, err := parseSupportedGroupsData(groupBody)
			if err != nil {
				return err
			}
			return ValidateSupportedGroups(groups)
		}
	}

	groups, err := groupsFrom(value)
	if err != nil {
		return err
	}
	if isKeyShareLike(value) {
		return ValidateKeyShareGroups(groups)
	}
	return ValidateSupportedGroups(groups)
}

// CheckDuplicateGroups is an alias for ValidateGroups.
func CheckDuplicateGroups(value any) error {
	return ValidateGroups(value)
}

func groupsFrom(value any) ([]NamedGroup, error) {
	if value == nil {
		return nil, nil
	}
	if raw, ok := value.([]byte); ok {
		return groupsFromBytes(raw)
	}
	if raw, ok := value.(string); ok {
		return groupsFromBytes([]byte(raw))
	}

	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("%w: cannot read groups from %T", ErrMalformedClientHello, value)
	}
	if rv.Len() == 0 {
		return nil, nil
	}

	elem := rv.Type().Elem()
	if elem.Kind() == reflect.Uint8 {
		raw := make([]byte, rv.Len())
		for i := range raw {
			raw[i] = byte(rv.Index(i).Uint())
		}
		return groupsFromBytes(raw)
	}
	if elem.Kind() >= reflect.Int && elem.Kind() <= reflect.Uint64 {
		groups := make([]NamedGroup, rv.Len())
		for i := range groups {
			groups[i] = NamedGroup(rv.Index(i).Uint())
		}
		return groups, nil
	}
	if elem.Kind() == reflect.Struct {
		field, ok := elem.FieldByName("Group")
		if ok && field.Type.Kind() >= reflect.Int && field.Type.Kind() <= reflect.Uint64 {
			groups := make([]NamedGroup, rv.Len())
			for i := range groups {
				groups[i] = NamedGroup(rv.Index(i).FieldByIndex(field.Index).Uint())
			}
			return groups, nil
		}
	}
	return nil, fmt.Errorf("%w: unsupported group element type %s", ErrMalformedClientHello, elem)
}

func groupsFromBytes(raw []byte) ([]NamedGroup, error) {
	raw = stripExtensionHeader(raw, ExtensionSupportedGroups)
	raw = stripExtensionHeader(raw, ExtensionKeyShare)
	if len(raw) >= 2 {
		listLen := int(binary.BigEndian.Uint16(raw[:2]))
		if listLen == len(raw)-2 && listLen%2 == 0 {
			return parseSupportedGroupsData(raw)
		}
	}
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("%w: group list has an odd length", ErrMalformedClientHello)
	}
	groups := make([]NamedGroup, 0, len(raw)/2)
	for offset := 0; offset < len(raw); offset += 2 {
		groups = append(groups, binary.BigEndian.Uint16(raw[offset:offset+2]))
	}
	return groups, nil
}

func isKeyShareLike(value any) bool {
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return false
	}
	elem := rv.Type().Elem()
	if elem.Kind() != reflect.Struct {
		return false
	}
	if _, ok := elem.FieldByName("Group"); !ok {
		return false
	}
	if _, ok := elem.FieldByName("KeyExchange"); ok {
		return true
	}
	_, ok := elem.FieldByName("Data")
	return ok
}

// Marshal encodes c as a TLS ClientHello body (without a record or handshake
// header). SupportedGroups and KeyShares are emitted as extensions in wire
// order; entries already present in Extensions for those two types are
// replaced by the structured fields.
func (c *ClientHello) Marshal() ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil ClientHello", ErrMalformedClientHello)
	}

	version := c.LegacyVersion
	if version == 0 {
		version = c.Version
	}
	if version == 0 {
		version = 0x0303
	}

	cipherSuites := c.CipherSuites
	if len(cipherSuites) == 0 {
		cipherSuites = []uint16{0x1301, 0x1302, 0x1303}
	}
	compressionMethods := c.CompressionMethods
	if len(compressionMethods) == 0 {
		compressionMethods = []byte{0}
	}
	sessionID := c.SessionID
	if len(sessionID) == 0 {
		sessionID = c.SessionId
	}
	supportedGroups := effectiveSupportedGroups(c)
	keyShares := effectiveKeyShares(c)

	body := make([]byte, 0, 2+32+1+len(sessionID)+2+2*len(cipherSuites)+1+len(compressionMethods)+2+128)
	var two [2]byte
	binary.BigEndian.PutUint16(two[:], version)
	body = append(body, two[:]...)
	body = append(body, c.Random[:]...)
	if len(sessionID) > 255 {
		return nil, fmt.Errorf("%w: session_id is too long", ErrMalformedClientHello)
	}
	body = append(body, byte(len(sessionID)))
	body = append(body, sessionID...)
	if len(cipherSuites) == 0 || len(cipherSuites) > 0xffff/2 {
		return nil, fmt.Errorf("%w: invalid cipher_suites", ErrMalformedClientHello)
	}
	binary.BigEndian.PutUint16(two[:], uint16(2*len(cipherSuites)))
	body = append(body, two[:]...)
	for _, suite := range cipherSuites {
		binary.BigEndian.PutUint16(two[:], suite)
		body = append(body, two[:]...)
	}
	if len(compressionMethods) == 0 || len(compressionMethods) > 255 {
		return nil, fmt.Errorf("%w: invalid compression_methods", ErrMalformedClientHello)
	}
	body = append(body, byte(len(compressionMethods)))
	body = append(body, compressionMethods...)

	extensions := make([]Extension, 0, len(c.Extensions)+2)
	for _, ext := range c.Extensions {
		if ext.Type == ExtensionSupportedGroups && len(supportedGroups) > 0 {
			continue
		}
		if ext.Type == ExtensionKeyShare && len(keyShares) > 0 {
			continue
		}
		extensions = append(extensions, ext)
	}
	if len(supportedGroups) > 0 {
		extData := make([]byte, 2+2*len(supportedGroups))
		binary.BigEndian.PutUint16(extData[:2], uint16(2*len(supportedGroups)))
		for i, group := range supportedGroups {
			binary.BigEndian.PutUint16(extData[2+2*i:], group)
		}
		extensions = append(extensions, Extension{Type: ExtensionSupportedGroups, Data: extData})
	}
	if len(keyShares) > 0 {
		extData := make([]byte, 2, 2+len(keyShares)*64)
		for _, share := range keyShares {
			keyExchange := share.KeyExchange
			if len(keyExchange) == 0 {
				keyExchange = share.Data
			}
			if len(keyExchange) > 0xffff {
				return nil, fmt.Errorf("%w: key exchange is too long", ErrMalformedClientHello)
			}
			var entry [4]byte
			binary.BigEndian.PutUint16(entry[:2], share.Group)
			binary.BigEndian.PutUint16(entry[2:4], uint16(len(keyExchange)))
			extData = append(extData, entry[:]...)
			extData = append(extData, keyExchange...)
		}
		binary.BigEndian.PutUint16(extData[:2], uint16(len(extData)-2))
		extensions = append(extensions, Extension{Type: ExtensionKeyShare, Data: extData})
	}

	if len(extensions) > 0 || offsetNeedsExtensions(c) {
		extensionsLen := 0
		for _, ext := range extensions {
			if len(ext.Data) > 0xffff {
				return nil, fmt.Errorf("%w: extension 0x%04x is too long", ErrMalformedClientHello, ext.Type)
			}
			extensionsLen += 4 + len(ext.Data)
		}
		if extensionsLen > 0xffff {
			return nil, fmt.Errorf("%w: extensions are too long", ErrMalformedClientHello)
		}
		binary.BigEndian.PutUint16(two[:], uint16(extensionsLen))
		body = append(body, two[:]...)
		for _, ext := range extensions {
			binary.BigEndian.PutUint16(two[:], ext.Type)
			body = append(body, two[:]...)
			binary.BigEndian.PutUint16(two[:], uint16(len(ext.Data)))
			body = append(body, two[:]...)
			body = append(body, ext.Data...)
		}
	}

	return body, nil
}

func offsetNeedsExtensions(c *ClientHello) bool {
	return len(c.Extensions) > 0 || len(effectiveSupportedGroups(c)) > 0 || len(effectiveKeyShares(c)) > 0
}

func effectiveSupportedGroups(c *ClientHello) []NamedGroup {
	if len(c.SupportedGroups) > 0 {
		return c.SupportedGroups
	}
	return c.SupportedCurves
}

func effectiveKeyShares(c *ClientHello) []KeyShareEntry {
	if len(c.KeyShares) > 0 {
		return c.KeyShares
	}
	return c.KeyShare
}

// MarshalClientHello encodes a ClientHello or *ClientHello as a TLS
// ClientHello body.
func MarshalClientHello(value any) ([]byte, error) {
	switch ch := value.(type) {
	case nil:
		return nil, fmt.Errorf("%w: nil ClientHello", ErrMalformedClientHello)
	case *ClientHello:
		return ch.Marshal()
	case ClientHello:
		return ch.Marshal()
	default:
		return nil, fmt.Errorf("%w: unsupported ClientHello type %T", ErrMalformedClientHello, value)
	}
}

// EncodeClientHello is an alias for MarshalClientHello.
func EncodeClientHello(value any) ([]byte, error) {
	return MarshalClientHello(value)
}

// Encode is an alias for MarshalClientHello.
func Encode(value any) ([]byte, error) {
	return MarshalClientHello(value)
}

// MarshalHandshake encodes c as a TLS handshake message, including the
// four-byte ClientHello handshake header.
func (c *ClientHello) MarshalHandshake() ([]byte, error) {
	body, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	if len(body) > 0xffffff {
		return nil, fmt.Errorf("%w: ClientHello is too long", ErrMalformedClientHello)
	}
	handshake := make([]byte, 4, 4+len(body))
	handshake[0] = HandshakeClientHello
	handshake[1] = byte(len(body) >> 16)
	handshake[2] = byte(len(body) >> 8)
	handshake[3] = byte(len(body))
	return append(handshake, body...), nil
}

// ParseClientHelloBody decodes a bare ClientHello body.
func ParseClientHelloBody(data []byte) (*ClientHello, error) {
	return parseClientHelloBody(data)
}

// ClientHelloBody is kept for symmetry with Reader/Writer style APIs.
func ClientHelloBody(data []byte) ([]byte, error) {
	return clientHelloBody(bytes.Clone(data))
}
