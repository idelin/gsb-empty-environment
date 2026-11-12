package netsec

import (
	"errors"
	"fmt"
)

// Extension types that influence negotiation security. A duplicate of any
// of these is a fatal error; every other (unknown) type is preserved
// verbatim in wire order but never surfaced through the typed accessors,
// so it cannot participate in known semantics.
const (
	ExtServerName          uint16 = 0
	ExtSupportedGroups     uint16 = 10
	ExtSignatureAlgorithms uint16 = 13
	ExtALPN                uint16 = 16
	ExtPreSharedKey        uint16 = 41
	ExtEarlyData           uint16 = 42
	ExtSupportedVersions   uint16 = 43
	ExtCookie              uint16 = 44
	ExtPSKKeyExchangeModes uint16 = 45
	ExtKeyShare            uint16 = 51
)

var (
	ErrTruncated          = errors.New("netsec: truncated extension block")
	ErrDuplicateExtension = errors.New("netsec: duplicate security-critical extension")
	ErrMalformed          = errors.New("netsec: malformed extension data")
)

var securityCritical = map[uint16]struct{}{
	ExtServerName:          {},
	ExtSupportedGroups:     {},
	ExtSignatureAlgorithms: {},
	ExtALPN:                {},
	ExtPreSharedKey:        {},
	ExtEarlyData:           {},
	ExtSupportedVersions:   {},
	ExtCookie:              {},
	ExtPSKKeyExchangeModes: {},
	ExtKeyShare:            {},
}

// Extension is a single extension exactly as it appeared on the wire.
type Extension struct {
	Type uint16
	Data []byte
}

// Table is an ordered, fully validated view of one extension block. It is
// built once by ParseExtensions and never mutated afterwards, so the
// parsing layer and the policy layer always observe the same unique
// instance of every field.
type Table struct {
	order []Extension
	known map[uint16]int // known extension type -> index into order

	serverName    string
	hasServerName bool
	alpn          []string
	hasALPN       bool
}

// ParseExtensions parses a TLS extension block (the concatenated
// type(2) || length(2) || data sequence carried in a handshake message)
// into a Table. Every length prefix, including the nested ones inside
// server_name and ALPN, is validated before the table is published; on
// any failure no table is returned.
func ParseExtensions(block []byte) (*Table, error) {
	t := &Table{known: make(map[uint16]int)}
	for i := 0; i < len(block); {
		if len(block)-i < 4 {
			return nil, fmt.Errorf("%w: header at offset %d", ErrTruncated, i)
		}
		extType := uint16(block[i])<<8 | uint16(block[i+1])
		dataLen := int(block[i+2])<<8 | int(block[i+3])
		i += 4
		if len(block)-i < dataLen {
			return nil, fmt.Errorf("%w: data of extension %d", ErrTruncated, extType)
		}
		data := make([]byte, dataLen)
		copy(data, block[i:i+dataLen])
		i += dataLen

		if _, critical := securityCritical[extType]; critical {
			if _, dup := t.known[extType]; dup {
				return nil, fmt.Errorf("%w: type %d", ErrDuplicateExtension, extType)
			}
			t.known[extType] = len(t.order)
		}
		t.order = append(t.order, Extension{Type: extType, Data: data})
	}
	if err := t.decodeKnown(); err != nil {
		return nil, err
	}
	return t, nil
}

// decodeKnown decodes the nested structures of known extensions exactly
// once, so downstream policy code can never re-parse a different view.
func (t *Table) decodeKnown() error {
	if idx, ok := t.known[ExtServerName]; ok {
		name, err := parseSNI(t.order[idx].Data)
		if err != nil {
			return err
		}
		t.serverName = name
		t.hasServerName = name != ""
	}
	if idx, ok := t.known[ExtALPN]; ok {
		protos, err := parseALPN(t.order[idx].Data)
		if err != nil {
			return err
		}
		t.alpn = protos
		t.hasALPN = true
	}
	return nil
}

// All returns every extension in wire order, including unknown types.
func (t *Table) All() []Extension {
	out := make([]Extension, len(t.order))
	copy(out, t.order)
	return out
}

// Get returns the unique known extension of the given type. Unknown types
// are never returned here: they are preserved in All but take no part in
// known semantics.
func (t *Table) Get(extType uint16) (Extension, bool) {
	idx, ok := t.known[extType]
	if !ok {
		return Extension{}, false
	}
	return t.order[idx], true
}

// ServerName returns the host_name decoded from the unique server_name
// extension at parse time. ok is false when the extension was absent or
// carried an empty name list.
func (t *Table) ServerName() (string, bool) {
	return t.serverName, t.hasServerName
}

// ALPNProtocols returns the protocol list decoded from the unique ALPN
// extension at parse time.
func (t *Table) ALPNProtocols() ([]string, bool) {
	if !t.hasALPN {
		return nil, false
	}
	out := make([]string, len(t.alpn))
	copy(out, t.alpn)
	return out, true
}

// parseSNI validates and decodes a server_name extension body:
// list_len(2) || { name_type(1) || name_len(2) || name }...
func parseSNI(data []byte) (string, error) {
	if len(data) < 2 {
		return "", fmt.Errorf("%w: server_name list length", ErrTruncated)
	}
	listLen := int(data[0])<<8 | int(data[1])
	body := data[2:]
	if listLen != len(body) {
		return "", fmt.Errorf("%w: server_name list declares %d bytes, carries %d", ErrMalformed, listLen, len(body))
	}
	seenTypes := map[byte]struct{}{}
	var host string
	for i := 0; i < len(body); {
		if len(body)-i < 3 {
			return "", fmt.Errorf("%w: server_name entry header", ErrTruncated)
		}
		nameType := body[i]
		nameLen := int(body[i+1])<<8 | int(body[i+2])
		i += 3
		if len(body)-i < nameLen {
			return "", fmt.Errorf("%w: server_name entry", ErrTruncated)
		}
		name := body[i : i+nameLen]
		i += nameLen
		if nameLen == 0 {
			return "", fmt.Errorf("%w: empty server_name entry", ErrMalformed)
		}
		if _, dup := seenTypes[nameType]; dup {
			return "", fmt.Errorf("%w: repeated server_name type %d", ErrMalformed, nameType)
		}
		seenTypes[nameType] = struct{}{}
		if nameType == 0 { // host_name
			host = string(name)
		}
	}
	return host, nil
}

// parseALPN validates and decodes an ALPN extension body:
// list_len(2) || { proto_len(1) || proto }...
func parseALPN(data []byte) ([]string, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: ALPN list length", ErrTruncated)
	}
	listLen := int(data[0])<<8 | int(data[1])
	body := data[2:]
	if listLen != len(body) {
		return nil, fmt.Errorf("%w: ALPN list declares %d bytes, carries %d", ErrMalformed, listLen, len(body))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty ALPN protocol list", ErrMalformed)
	}
	var protos []string
	for i := 0; i < len(body); {
		n := int(body[i])
		i++
		if n == 0 {
			return nil, fmt.Errorf("%w: empty ALPN protocol name", ErrMalformed)
		}
		if len(body)-i < n {
			return nil, fmt.Errorf("%w: ALPN protocol name", ErrTruncated)
		}
		protos = append(protos, string(body[i:i+n]))
		i += n
	}
	return protos, nil
}
