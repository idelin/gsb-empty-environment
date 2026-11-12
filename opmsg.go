package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"unicode/utf8"
)

const (
	// OpMsg is the MongoDB wire protocol opcode for OP_MSG.
	OpMsg int32 = 2013

	// FlagChecksumPresent indicates that the message ends with a CRC-32C checksum.
	FlagChecksumPresent uint32 = 1 << 0
	// FlagMoreToCome indicates that another message will follow.
	FlagMoreToCome uint32 = 1 << 1
	// FlagExhaustAllowed indicates that the client accepts exhaust cursors.
	FlagExhaustAllowed uint32 = 1 << 2

	knownRequiredFlags uint32 = FlagChecksumPresent | FlagMoreToCome | FlagExhaustAllowed
	msgHeaderSize             = 16
	opMsgPrefixSize           = msgHeaderSize + 4
	maxBSONDepth              = 100
)

var (
	// ErrNotOPMsg indicates that the wire message is not an OP_MSG.
	ErrNotOPMsg = errors.New("netsec: not an OP_MSG")
	// ErrMalformedOPMsg indicates a broken message header or section layout.
	ErrMalformedOPMsg = errors.New("netsec: malformed OP_MSG")
	// ErrUnknownFlagBits indicates a set reserved bit in the low 16 flag bits.
	ErrUnknownFlagBits = errors.New("netsec: unknown OP_MSG flag bits")
	// ErrBadChecksum indicates that the CRC-32C trailer does not match the message.
	ErrBadChecksum = errors.New("netsec: bad OP_MSG CRC-32C checksum")
	// ErrMalformedBSON indicates an invalid or truncated BSON document.
	ErrMalformedBSON = errors.New("netsec: malformed BSON")
	// ErrUnauthenticatedCommand indicates a command outside the pre-auth allowlist.
	ErrUnauthenticatedCommand = errors.New("netsec: command is not allowed before authentication")
	// ErrInvalidDatabase indicates that $db is missing, not a BSON string, or not "admin".
	ErrInvalidDatabase = errors.New("netsec: pre-auth command requires $db string \"admin\"")
)

var preAuthCommands = map[string]struct{}{
	"hello":        {},
	"isMaster":     {},
	"saslStart":    {},
	"saslContinue": {},
}

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// PreAuthMessage is the result of validating an OP_MSG received before
// authentication. Command is the first top-level BSON key in the sole kind 0
// body section.
type PreAuthMessage struct {
	Command  string
	Database string
	Flags    uint32
}

// InspectPreAuthOPMsg validates an unauthenticated OP_MSG and returns the
// command and database selected by the protocol. The function is deliberately
// strict: unknown required flag bits, malformed sections, bad checksums and
// duplicate BSON keys are rejected before a command can be dispatched.
func InspectPreAuthOPMsg(message []byte) (PreAuthMessage, error) {
	if len(message) < opMsgPrefixSize {
		return PreAuthMessage{}, fmt.Errorf("%w: message is shorter than the OP_MSG prefix", ErrMalformedOPMsg)
	}

	messageLength := int(int32(binary.LittleEndian.Uint32(message[0:4])))
	if messageLength != len(message) {
		return PreAuthMessage{}, fmt.Errorf("%w: declared messageLength %d does not match wire length %d", ErrMalformedOPMsg, messageLength, len(message))
	}
	if opcode := int32(binary.LittleEndian.Uint32(message[12:16])); opcode != OpMsg {
		return PreAuthMessage{}, fmt.Errorf("%w: opcode is %d, want %d", ErrNotOPMsg, opcode, OpMsg)
	}

	flags := binary.LittleEndian.Uint32(message[16:20])
	if unknown := flags & 0xffff &^ knownRequiredFlags; unknown != 0 {
		return PreAuthMessage{}, fmt.Errorf("%w: bits 0x%04x", ErrUnknownFlagBits, unknown)
	}

	contentEnd := len(message)
	if flags&FlagChecksumPresent != 0 {
		if contentEnd < opMsgPrefixSize+6 {
			return PreAuthMessage{}, fmt.Errorf("%w: checksumPresent with no complete body and checksum", ErrMalformedOPMsg)
		}
		payload := message[:contentEnd-4]
		want := binary.LittleEndian.Uint32(message[contentEnd-4:])
		got := crc32.Checksum(payload, crc32cTable)
		if got != want {
			return PreAuthMessage{}, fmt.Errorf("%w: got 0x%08x, want 0x%08x", ErrBadChecksum, got, want)
		}
		contentEnd -= 4
	}

	command, database, err := parseOPMsgSections(message[opMsgPrefixSize:contentEnd])
	if err != nil {
		return PreAuthMessage{}, err
	}
	if _, ok := preAuthCommands[command]; !ok {
		return PreAuthMessage{}, fmt.Errorf("%w: %q", ErrUnauthenticatedCommand, command)
	}
	if database != "admin" {
		return PreAuthMessage{}, fmt.Errorf("%w: got %q", ErrInvalidDatabase, database)
	}

	return PreAuthMessage{
		Command:  command,
		Database: database,
		Flags:    flags,
	}, nil
}

// CheckPreAuthOPMsg is the error-only entry point for a proxy gate.
func CheckPreAuthOPMsg(message []byte) error {
	_, err := InspectPreAuthOPMsg(message)
	return err
}

// ValidatePreAuthOPMsg is an alias for CheckPreAuthOPMsg.
func ValidatePreAuthOPMsg(message []byte) error {
	return CheckPreAuthOPMsg(message)
}

// IsPreAuthCommand reports whether command is allowed before authentication.
// The comparison is case-sensitive, as MongoDB command names are case-sensitive.
func IsPreAuthCommand(command string) bool {
	_, ok := preAuthCommands[command]
	return ok
}

type parsedDocument struct {
	firstKey string
	fields   map[string]bsonValue
}

type bsonValue struct {
	kind      byte
	stringV   string
	hasString bool
}

func parseOPMsgSections(data []byte) (command, database string, err error) {
	var body *parsedDocument
	bodyCount := 0
	sequenceIDs := make(map[string]struct{})

	for pos := 0; pos < len(data); {
		kind := data[pos]
		pos++

		switch kind {
		case 0:
			bodyCount++
			if bodyCount != 1 {
				return "", "", fmt.Errorf("%w: multiple kind 0 sections", ErrMalformedOPMsg)
			}

			doc, consumed, parseErr := parseDocument(data[pos:], 0)
			if parseErr != nil {
				return "", "", parseErr
			}
			body = doc
			pos += consumed
			if pos != len(data) {
				return "", "", fmt.Errorf("%w: data follows the kind 0 body section", ErrMalformedOPMsg)
			}

		case 1:
			if len(data)-pos < 4 {
				return "", "", fmt.Errorf("%w: kind 1 section has no size", ErrMalformedOPMsg)
			}
			sectionSize := int(int32(binary.LittleEndian.Uint32(data[pos : pos+4])))
			sectionStart := pos
			if sectionSize < 5 {
				return "", "", fmt.Errorf("%w: kind 1 section size %d is too small", ErrMalformedOPMsg, sectionSize)
			}
			if sectionSize > len(data)-sectionStart {
				return "", "", fmt.Errorf("%w: kind 1 section size %d exceeds %d remaining bytes", ErrMalformedOPMsg, sectionSize, len(data)-sectionStart)
			}
			sectionEnd := sectionStart + sectionSize

			identifierStart := sectionStart + 4
			identifier, afterIdentifier, idErr := readCString(data, identifierStart, sectionEnd)
			if idErr != nil {
				return "", "", fmt.Errorf("%w: kind 1 identifier: %v", ErrMalformedOPMsg, idErr)
			}
			if identifier == "" {
				return "", "", fmt.Errorf("%w: kind 1 section has an empty identifier", ErrMalformedOPMsg)
			}
			if _, duplicate := sequenceIDs[identifier]; duplicate {
				return "", "", fmt.Errorf("%w: duplicate kind 1 identifier %q", ErrMalformedOPMsg, identifier)
			}
			sequenceIDs[identifier] = struct{}{}

			for docPos := afterIdentifier; docPos < sectionEnd; {
				_, consumed, parseErr := parseDocument(data[docPos:sectionEnd], 0)
				if parseErr != nil {
					return "", "", parseErr
				}
				docPos += consumed
			}
			pos = sectionEnd

		default:
			return "", "", fmt.Errorf("%w: unknown section kind %d", ErrMalformedOPMsg, kind)
		}
	}

	if bodyCount != 1 || body == nil {
		return "", "", fmt.Errorf("%w: expected exactly one kind 0 section, got %d", ErrMalformedOPMsg, bodyCount)
	}
	if body.firstKey == "" {
		return "", "", fmt.Errorf("%w: command document has no first key", ErrMalformedOPMsg)
	}
	for identifier := range sequenceIDs {
		if _, conflict := body.fields[identifier]; conflict {
			return "", "", fmt.Errorf("%w: kind 1 identifier %q duplicates a kind 0 field", ErrMalformedOPMsg, identifier)
		}
	}

	value, ok := body.fields["$db"]
	if !ok || !value.hasString {
		return "", "", ErrInvalidDatabase
	}
	return body.firstKey, value.stringV, nil
}

func parseDocument(data []byte, depth int) (*parsedDocument, int, error) {
	if depth > maxBSONDepth {
		return nil, 0, fmt.Errorf("%w: nesting exceeds %d levels", ErrMalformedBSON, maxBSONDepth)
	}
	if len(data) < 5 {
		return nil, 0, fmt.Errorf("%w: document is shorter than 5 bytes", ErrMalformedBSON)
	}

	size := int(int32(binary.LittleEndian.Uint32(data[0:4])))
	if size < 5 || size > len(data) {
		return nil, 0, fmt.Errorf("%w: document size %d exceeds %d available bytes", ErrMalformedBSON, size, len(data))
	}

	doc := &parsedDocument{fields: make(map[string]bsonValue)}
	content := data[:size]
	terminator := size - 1
	if content[terminator] != 0 {
		return nil, 0, fmt.Errorf("%w: document terminator is missing", ErrMalformedBSON)
	}

	pos := 4
	for pos < terminator {
		kind := content[pos]
		pos++

		key, next, err := readCString(content, pos, terminator)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: field name: %v", ErrMalformedBSON, err)
		}
		if key == "" {
			return nil, 0, fmt.Errorf("%w: empty field name", ErrMalformedBSON)
		}
		if _, duplicate := doc.fields[key]; duplicate {
			return nil, 0, fmt.Errorf("%w: duplicate field name %q", ErrMalformedBSON, key)
		}
		if doc.firstKey == "" {
			doc.firstKey = key
		}
		pos = next

		valueStart := pos
		pos, err = skipValue(content, pos, kind, depth, terminator)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: field %q: %v", ErrMalformedBSON, key, err)
		}

		value := bsonValue{kind: kind}
		if isStringType(kind) {
			value.stringV, _, err = parseString(content, valueStart, terminator)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: field %q: %v", ErrMalformedBSON, key, err)
			}
			value.hasString = true
		}
		doc.fields[key] = value
	}

	if pos != terminator {
		return nil, 0, fmt.Errorf("%w: document has %d unparsed bytes", ErrMalformedBSON, terminator-pos)
	}
	return doc, size, nil
}

func skipValue(data []byte, pos int, kind byte, depth, limit int) (int, error) {
	if pos > limit {
		return 0, errors.New("value starts past document boundary")
	}
	need := func(n int) error {
		if n < 0 || pos+n > limit {
			return fmt.Errorf("value needs %d bytes at offset %d", n, pos)
		}
		return nil
	}

	switch kind {
	case 0x01: // double
		if err := need(8); err != nil {
			return 0, err
		}
		return pos + 8, nil
	case 0x02, 0x0d, 0x0e: // string, JavaScript code, symbol
		_, next, err := parseString(data, pos, limit)
		return next, err
	case 0x03, 0x04: // embedded document, array
		_, consumed, err := parseDocument(data[pos:], depth+1)
		if err != nil {
			return 0, err
		}
		if pos+consumed > limit {
			return 0, errors.New("embedded document crosses parent boundary")
		}
		return pos + consumed, nil
	case 0x05: // binary
		if err := need(5); err != nil {
			return 0, err
		}
		length := int(int32(binary.LittleEndian.Uint32(data[pos : pos+4])))
		if length < 0 {
			return 0, errors.New("negative binary length")
		}
		if pos+5+length > limit {
			return 0, errors.New("binary value crosses document boundary")
		}
		return pos + 5 + length, nil
	case 0x06, 0x0a, 0x7f, 0xff: // undefined, null, max key, min key
		return pos, nil
	case 0x07: // ObjectID
		if err := need(12); err != nil {
			return 0, err
		}
		return pos + 12, nil
	case 0x08: // boolean
		if err := need(1); err != nil {
			return 0, err
		}
		return pos + 1, nil
	case 0x09, 0x11, 0x12: // UTC datetime, timestamp, int64
		if err := need(8); err != nil {
			return 0, err
		}
		return pos + 8, nil
	case 0x0b: // regular expression
		_, next, err := readCString(data, pos, limit)
		if err != nil {
			return 0, err
		}
		_, next, err = readCString(data, next, limit)
		if err != nil {
			return 0, err
		}
		return next, nil
	case 0x0c: // DBPointer
		_, next, err := parseString(data, pos, limit)
		if err != nil {
			return 0, err
		}
		if next+12 > limit {
			return 0, errors.New("DBPointer crosses document boundary")
		}
		return next + 12, nil
	case 0x0f: // code with scope
		if err := need(4); err != nil {
			return 0, err
		}
		total := int(int32(binary.LittleEndian.Uint32(data[pos : pos+4])))
		if total < 14 || pos+total > limit {
			return 0, fmt.Errorf("invalid code-with-scope size %d", total)
		}
		innerStart := pos + 4
		innerEnd := pos + total
		codeEnd, err := skipStringWithin(data, innerStart, innerEnd)
		if err != nil {
			return 0, err
		}
		if codeEnd+5 > innerEnd {
			return 0, errors.New("code-with-scope has no room for a document")
		}
		_, consumed, err := parseDocument(data[codeEnd:innerEnd], depth+1)
		if err != nil {
			return 0, err
		}
		if codeEnd+consumed != innerEnd {
			return 0, errors.New("code-with-scope document does not fill the value")
		}
		return innerEnd, nil
	case 0x10: // int32
		if err := need(4); err != nil {
			return 0, err
		}
		return pos + 4, nil
	case 0x13: // decimal128
		if err := need(16); err != nil {
			return 0, err
		}
		return pos + 16, nil
	default:
		return 0, fmt.Errorf("unknown BSON type 0x%02x", kind)
	}
}

func parseString(data []byte, pos, limit int) (string, int, error) {
	if pos < 0 || pos+4 > limit {
		return "", 0, errors.New("string has no length field")
	}
	length := int(int32(binary.LittleEndian.Uint32(data[pos : pos+4])))
	if length < 1 {
		return "", 0, fmt.Errorf("invalid string length %d", length)
	}
	end := pos + 4 + length
	if end > limit || data[end-1] != 0 {
		return "", 0, errors.New("string crosses document boundary or is unterminated")
	}
	value := string(data[pos+4 : end-1])
	if !utf8.ValidString(value) {
		return "", 0, errors.New("string is not valid UTF-8")
	}
	return value, end, nil
}

func skipStringWithin(data []byte, pos, limit int) (int, error) {
	_, next, err := parseString(data, pos, limit)
	return next, err
}

func readCString(data []byte, pos, limit int) (string, int, error) {
	if pos < 0 || pos >= limit {
		return "", 0, errors.New("cstring starts outside its boundary")
	}
	for next := pos; next < limit; next++ {
		if data[next] != 0 {
			continue
		}
		value := string(data[pos:next])
		if !utf8.ValidString(value) {
			return "", 0, errors.New("cstring is not valid UTF-8")
		}
		return value, next + 1, nil
	}
	return "", 0, errors.New("cstring is unterminated")
}

func isStringType(kind byte) bool {
	return kind == 0x02
}
