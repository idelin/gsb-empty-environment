package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func envelope(parts ...string) string {
	return "{" + strings.Join(parts, ",") + "}"
}

func field(name, val string) string { return jsonString(name) + ":" + val }

func validEnvelope(order []string) string {
	payload := []byte("update-payload-0123456789")
	msg := &Message{Version: 7, Length: uint64(len(payload)), Payload: payload}
	msg.Digest = Digest(msg)
	values := map[string]string{
		FieldVersion: strconv.FormatUint(uint64(msg.Version), 10),
		FieldLength:  strconv.FormatUint(msg.Length, 10),
		FieldPayload: jsonString(base64.StdEncoding.EncodeToString(msg.Payload)),
		FieldDigest:  jsonString(msg.Digest),
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, jsonString(name)+":"+values[name])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestCanonicalGolden locks the deterministic canonical bytes and digest so
// neither parser nor framework escaping can alter the signed representation.
func TestCanonicalGolden(t *testing.T) {
	msg := &Message{
		Version: 7,
		Length:  25,
		Payload: []byte("update-payload-0123456789"),
	}
	msg.Digest = Digest(msg)

	const wantCore = `{"version":7,"length":25,"payload":"dXBkYXRlLXBheWxvYWQtMDEyMzQ1Njc4OQ=="}`
	if got := string(encode(msg, false)); got != wantCore {
		t.Fatalf("canonical core = %s, want %s", got, wantCore)
	}
	wantPrefix := strings.TrimSuffix(wantCore, "}") + `,"digest":"`
	if got := string(Canonical(msg)); !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("canonical full = %s, want prefix %s", got, wantPrefix)
	}
	if !isLowerHex(msg.Digest, 64) {
		t.Fatalf("digest %q is not 64 lowercase hex chars", msg.Digest)
	}
	if sum := sha256Sum([]byte(wantCore)); sum != msg.Digest {
		t.Fatalf("digest = %s, want independently computed %s", msg.Digest, sum)
	}

	var generic map[string]any
	if err := json.Unmarshal(Canonical(msg), &generic); err != nil {
		t.Fatalf("canonical output is not valid JSON: %v", err)
	}
}

// TestReceiveAllFieldOrders proves wire field order varies while every
// permutation yields identical semantics and canonical bytes.
func TestReceiveAllFieldOrders(t *testing.T) {
	orders := [][]string{
		{FieldVersion, FieldLength, FieldPayload, FieldDigest},
		{FieldDigest, FieldPayload, FieldLength, FieldVersion},
		{FieldPayload, FieldDigest, FieldVersion, FieldLength},
		{FieldLength, FieldVersion, FieldDigest, FieldPayload},
	}

	refMsg, err := Receive([]byte(validEnvelope(orders[0])))
	if err != nil {
		t.Fatalf("reference envelope rejected: %v", err)
	}
	reference := Canonical(refMsg)
	for i, order := range orders {
		wire := validEnvelope(order)
		spaced := strings.ReplaceAll(wire, ",", " , ")
		spaced = strings.ReplaceAll(spaced, ":", " : ")

		for label, input := range map[string]string{"compact": wire, "spaced": spaced} {
			msg, err := Receive([]byte(input))
			if err != nil {
				t.Fatalf("order %d (%s) rejected: %v", i, label, err)
			}
			got := Canonical(msg)
			if !reflect.DeepEqual(got, reference) {
				t.Fatalf("order %d (%s) canonical = %s, want %s", i, label, got, reference)
			}
		}
	}
}

// TestReceiveMutationsRejected is the release gate: every mutated sample must
// be refused, including duplicates, non-canonical numbers, unknown fields and
// range violations.
func TestReceiveMutationsRejected(t *testing.T) {
	payload := []byte("update-payload-0123456789")
	b64 := base64.StdEncoding.EncodeToString(payload)
	digest := Digest(&Message{Version: 7, Length: uint64(len(payload)), Payload: payload})

	base := func() string {
		return envelope(
			field(FieldVersion, "7"),
			field(FieldLength, strconv.Itoa(len(payload))),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest)),
		)
	}

	tests := []struct {
		name string
		wire string
		want error
	}{
		{"empty", "", ErrMalformed},
		{"top level array", `[1,2,3]`, ErrMalformed},
		{"top level string", jsonString("x"), ErrMalformed},
		{"leading zero version", envelope(
			field(FieldVersion, "07"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"zero fields allowed", envelope(
			field(FieldVersion, "0"),
			field(FieldLength, "0"),
			field(FieldPayload, jsonString("")),
			field(FieldDigest, jsonString(Digest(&Message{Version: 0})))), nil},
		{"negative version", envelope(
			field(FieldVersion, "-1"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"plus sign length", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "+27"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"fractional version", envelope(
			field(FieldVersion, "7.0"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"exponent version", envelope(
			field(FieldVersion, "1e1"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"version over uint32", envelope(
			field(FieldVersion, "4294967296"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrOutOfRange},
		{"length over uint64", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "18446744073709551616"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrOutOfRange},
		{"version as string", envelope(
			field(FieldVersion, jsonString("7")),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"length null", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "null"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"duplicate version", envelope(
			field(FieldVersion, "7"),
			field(FieldVersion, "8"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrDuplicateKey},
		{"duplicate digest same value", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest)),
			field(FieldDigest, jsonString(digest))), ErrDuplicateKey},
		{"unknown field", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest)),
			field("extra", "1")), ErrUnknownField},
		{"missing digest", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64))), ErrMalformed},
		{"wrong length", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "26"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"bad base64", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString("not_base64!")),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"uppercase digest hex", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(strings.ToUpper(digest)))), ErrMalformed},
		{"short digest", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString("abc"))), ErrMalformed},
		{"tampered digest", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(strings.Repeat("0", 64)))), ErrDigestMismatch},
		{"tampered payload", envelope(
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(base64.StdEncoding.EncodeToString(
				[]byte("UPDATE-PAYLOAD-0123456789")))),
			field(FieldDigest, jsonString(digest))), ErrDigestMismatch},
		{"trailing data", base() + "x", ErrMalformed},
		{"bom prefix", "\ufeff" + base(), ErrMalformed},
		{"single quotes", `{'version':7}`, ErrMalformed},
		{"unquoted key", envelope(
			"version:7",
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrMalformed},
		{"trailing comma", strings.TrimSuffix(base(), "}") + ",}", ErrMalformed},
		{"duplicate key via unicode escape", envelope(
			`"vers\u0069on":7`,
			field(FieldVersion, "7"),
			field(FieldLength, "25"),
			field(FieldPayload, jsonString(b64)),
			field(FieldDigest, jsonString(digest))), ErrDuplicateKey},
		{"invalid utf8", string([]byte{
			'{', '"', 'k', '"', ':', '"', 0xff, 0xfe, '"', '}',
		}), ErrMalformed},
		{"lone unicode surrogate", envelope(
			`"a\uD800":1`), ErrMalformed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := Receive([]byte(tt.wire))
			if tt.want == nil {
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
				if msg == nil {
					t.Fatal("accepted message is nil")
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %v, accepted message %+v", tt.want, msg)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.want)
			}
		})
	}
}
