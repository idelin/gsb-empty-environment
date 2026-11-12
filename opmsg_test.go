package netsec

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

func TestAllowedPreAuthCommands(t *testing.T) {
	for _, command := range []string{"hello", "isMaster", "saslStart", "saslContinue"} {
		t.Run(command, func(t *testing.T) {
			message := buildOPMsg(0, kind0(commandDocument(command)))
			parsed, err := InspectPreAuthOPMsg(message)
			if err != nil {
				t.Fatalf("InspectPreAuthOPMsg() error = %v", err)
			}
			if parsed.Command != command || parsed.Database != "admin" {
				t.Fatalf("parsed = %#v", parsed)
			}
			if err := CheckPreAuthOPMsg(message); err != nil {
				t.Fatalf("CheckPreAuthOPMsg() error = %v", err)
			}
		})
	}
}

func TestCommandNamesAreCaseSensitive(t *testing.T) {
	for _, command := range []string{"Hello", "HELLO", "ismaster", "IsMaster", "saslstart", "SASLContinue", "hello "} {
		t.Run(command, func(t *testing.T) {
			err := CheckPreAuthOPMsg(buildOPMsg(0, kind0(commandDocument(command))))
			if !errors.Is(err, ErrUnauthenticatedCommand) {
				t.Fatalf("error = %v, want %v", err, ErrUnauthenticatedCommand)
			}
		})
	}
}

func TestCommandMustHaveAdminDatabase(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want error
	}{
		{
			name: "missing db",
			body: bsonDoc(bsonInt32("hello", 1)),
			want: ErrInvalidDatabase,
		},
		{
			name: "db is not a string",
			body: bsonDoc(bsonInt32("hello", 1), bsonInt32("$db", 1)),
			want: ErrInvalidDatabase,
		},
		{
			name: "db has wrong case",
			body: bsonDoc(bsonInt32("hello", 1), bsonString("$db", "Admin")),
			want: ErrInvalidDatabase,
		},
		{
			name: "db is a BSON symbol",
			body: bsonDoc(bsonInt32("hello", 1), bsonSymbol("$db", "admin")),
			want: ErrInvalidDatabase,
		},
		{
			name: "duplicate db",
			body: bsonDoc(bsonInt32("hello", 1), bsonString("$db", "admin"), bsonString("$db", "admin")),
			want: ErrMalformedBSON,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPreAuthOPMsg(buildOPMsg(0, kind0(tc.body)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUnknownLowFlagBitsAreRejected(t *testing.T) {
	for _, flags := range []uint32{1 << 3, 1 << 7, 1 << 15, 1<<3 | 1<<31} {
		message := buildOPMsg(flags, kind0(commandDocument("hello")))
		err := CheckPreAuthOPMsg(message)
		if !errors.Is(err, ErrUnknownFlagBits) {
			t.Fatalf("flags 0x%08x: error = %v, want %v", flags, err, ErrUnknownFlagBits)
		}
	}
}

func TestKnownAndOptionalHighFlagBitsAreAccepted(t *testing.T) {
	for _, flags := range []uint32{FlagMoreToCome, FlagExhaustAllowed, 1 << 31, 0xffff0001} {
		message := buildOPMsg(flags, kind0(commandDocument("hello")))
		if err := CheckPreAuthOPMsg(message); err != nil {
			t.Fatalf("flags 0x%08x: error = %v", flags, err)
		}
	}
}

func TestChecksumValidation(t *testing.T) {
	message := buildOPMsg(FlagChecksumPresent, kind0(commandDocument("hello")))
	if err := CheckPreAuthOPMsg(message); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}

	bad := append([]byte(nil), message...)
	bad[len(bad)-1] ^= 0xff
	if err := CheckPreAuthOPMsg(bad); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("bad checksum: error = %v, want %v", err, ErrBadChecksum)
	}
}

func TestCRC32CCastagnoliVector(t *testing.T) {
	if got := crc32.Checksum([]byte("123456789"), crc32cTable); got != 0xe3069283 {
		t.Fatalf("CRC-32C = 0x%08x, want 0xe3069283", got)
	}
}

func TestMultipleBodySectionsAreRejected(t *testing.T) {
	message := buildOPMsg(0,
		kind0(commandDocument("hello")),
		kind0(commandDocument("dropDatabase")),
	)
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
	}
}

func TestExtraBytesAfterBodyAreRejected(t *testing.T) {
	message := buildOPMsg(0, kind0(commandDocument("hello")), []byte{0})
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
	}
}

func TestBodySectionMustBePresent(t *testing.T) {
	message := buildOPMsg(0, kind1("documents", commandDocument("dropDatabase")))
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
	}
}

func TestKind1SectionPrecedesBodyAndDoesNotChooseCommand(t *testing.T) {
	message := buildOPMsg(0,
		kind1("documents", commandDocument("dropDatabase")),
		kind0(commandDocument("hello")),
	)
	parsed, err := InspectPreAuthOPMsg(message)
	if err != nil {
		t.Fatalf("InspectPreAuthOPMsg() error = %v", err)
	}
	if parsed.Command != "hello" {
		t.Fatalf("command = %q, want hello", parsed.Command)
	}
}

func TestKind1IdentifierCannotDuplicateBodyField(t *testing.T) {
	message := buildOPMsg(0,
		kind1("$db", bsonDoc(bsonString("value", "admin"))),
		kind0(commandDocument("hello")),
	)
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
	}
}

func TestDuplicateKind1IdentifierIsRejected(t *testing.T) {
	message := buildOPMsg(0,
		kind1("documents", bsonDoc()),
		kind1("documents", bsonDoc()),
		kind0(commandDocument("hello")),
	)
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
	}
}

func TestKind1SectionSizeBounds(t *testing.T) {
	tests := []struct {
		name    string
		section []byte
	}{
		{
			name:    "size too small",
			section: []byte{1, 4, 0, 0, 0},
		},
		{
			name: "size extends past message",
			section: func() []byte {
				section := appendInt32([]byte{1}, 0x20000000)
				section = append(section, []byte("documents")...)
				section = append(section, 0)
				return append(section, commandDocument("hello")...)
			}(),
		},
		{
			name:    "size covers only part of document",
			section: shortKind1("documents", commandDocument("hello")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := buildOPMsg(0, tc.section, kind0(commandDocument("hello")))
			if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) && !errors.Is(err, ErrMalformedBSON) {
				t.Fatalf("error = %v, want malformed section/document", err)
			}
		})
	}
}

func TestKind1CannotConsumeChecksum(t *testing.T) {
	body := kind0(commandDocument("hello"))
	section := make([]byte, 4)
	binary.LittleEndian.PutUint32(section, uint32(len(body)+4))
	section = append(section, []byte("documents")...)
	section = append(section, 0)
	section = append(section, commandDocument("hello")...)

	message := buildOPMsg(FlagChecksumPresent, append([]byte{1}, section...), body)
	if err := CheckPreAuthOPMsg(message); !errors.Is(err, ErrMalformedOPMsg) && !errors.Is(err, ErrMalformedBSON) {
		t.Fatalf("error = %v, want a malformed section or document", err)
	}
}

func TestUnknownSectionKindAndBadBSON(t *testing.T) {
	t.Run("unknown kind", func(t *testing.T) {
		err := CheckPreAuthOPMsg(buildOPMsg(0, []byte{2}, commandDocument("hello")))
		if !errors.Is(err, ErrMalformedOPMsg) {
			t.Fatalf("error = %v, want %v", err, ErrMalformedOPMsg)
		}
	})

	t.Run("truncated bson", func(t *testing.T) {
		body := commandDocument("hello")
		err := CheckPreAuthOPMsg(buildOPMsg(0, append([]byte{0}, body[:len(body)-1]...)))
		if !errors.Is(err, ErrMalformedBSON) {
			t.Fatalf("error = %v, want %v", err, ErrMalformedBSON)
		}
	})
}

func TestMessageLengthMustMatch(t *testing.T) {
	message := buildOPMsg(0, kind0(commandDocument("hello")))
	for _, delta := range []int{-1, 1} {
		bad := append([]byte(nil), message...)
		binary.LittleEndian.PutUint32(bad[:4], uint32(len(bad)+delta))
		if err := CheckPreAuthOPMsg(bad); !errors.Is(err, ErrMalformedOPMsg) {
			t.Fatalf("delta %d: error = %v, want %v", delta, err, ErrMalformedOPMsg)
		}
	}
}

func commandDocument(command string) []byte {
	return bsonDoc(
		bsonInt32(command, 1),
		bsonString("$db", "admin"),
	)
}

func bsonDoc(elements ...[]byte) []byte {
	total := 4 + 1
	for _, element := range elements {
		total += len(element)
	}
	doc := make([]byte, 0, total)
	doc = appendInt32(doc, int32(total))
	for _, element := range elements {
		doc = append(doc, element...)
	}
	return append(doc, 0)
}

func bsonInt32(key string, value int32) []byte {
	element := []byte{0x10}
	element = append(element, key...)
	element = append(element, 0)
	return appendInt32(element, value)
}

func bsonString(key, value string) []byte {
	element := []byte{0x02}
	return appendBSONString(element, key, value)
}

func bsonSymbol(key, value string) []byte {
	element := []byte{0x0e}
	return appendBSONString(element, key, value)
}

func appendBSONString(element []byte, key, value string) []byte {
	element = append(element, key...)
	element = append(element, 0)
	element = appendInt32(element, int32(len(value)+1))
	element = append(element, value...)
	return append(element, 0)
}

func kind0(document []byte) []byte {
	return append([]byte{0}, document...)
}

func kind1(identifier string, documents ...[]byte) []byte {
	payload := append([]byte(identifier), 0)
	for _, document := range documents {
		payload = append(payload, document...)
	}
	section := []byte{1}
	section = appendInt32(section, int32(4+len(payload)))
	return append(section, payload...)
}

func shortKind1(identifier string, document []byte) []byte {
	payload := append([]byte(identifier), 0)
	payload = append(payload, document...)
	section := []byte{1}
	section = appendInt32(section, int32(len(document)-1))
	return append(section, payload...)
}

func buildOPMsg(flags uint32, sections ...[]byte) []byte {
	length := opMsgPrefixSize
	for _, section := range sections {
		length += len(section)
	}
	if flags&FlagChecksumPresent != 0 {
		length += 4
	}

	message := make([]byte, opMsgPrefixSize, length)
	binary.LittleEndian.PutUint32(message[0:4], uint32(length))
	binary.LittleEndian.PutUint32(message[4:8], 1)
	binary.LittleEndian.PutUint32(message[8:12], 0)
	binary.LittleEndian.PutUint32(message[12:16], uint32(OpMsg))
	binary.LittleEndian.PutUint32(message[16:20], flags)
	for _, section := range sections {
		message = append(message, section...)
	}

	if flags&FlagChecksumPresent != 0 {
		message = appendInt32(message, int32(crc32.Checksum(message, crc32cTable)))
	}
	return message
}

func appendInt32(data []byte, value int32) []byte {
	return append(data, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}

func TestErrorMessageIncludesCommand(t *testing.T) {
	err := CheckPreAuthOPMsg(buildOPMsg(0, kind0(commandDocument("dropDatabase"))))
	if err == nil || !strings.Contains(err.Error(), "dropDatabase") {
		t.Fatalf("error = %v", err)
	}
}
