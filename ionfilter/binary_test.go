package ionfilter

import (
	"bytes"
	"testing"
)

func encVarUInt(value int) []byte {
	var raw []byte
	for {
		raw = append([]byte{byte(value & 0x7F)}, raw...)
		value >>= 7
		if value == 0 {
			break
		}
	}
	raw[len(raw)-1] |= 0x80
	return raw
}

func encTLV(typeCode int, body []byte) []byte {
	out := []byte{byte(typeCode << 4)}
	if len(body) < 14 {
		out[0] |= byte(len(body))
	} else {
		out[0] |= 0x0E
		out = append(out, encVarUInt(len(body))...)
	}
	return append(out, body...)
}

func encString(text string) []byte {
	return encTLV(8, []byte(text))
}

func encSymbol(sid int) []byte {
	if sid == 0 {
		return []byte{0x70}
	}
	var body []byte
	for sid > 0 {
		body = append([]byte{byte(sid & 0xFF)}, body...)
		sid >>= 8
	}
	return encTLV(7, body)
}

func encList(items ...[]byte) []byte {
	var body bytes.Buffer
	for _, item := range items {
		body.Write(item)
	}
	return encTLV(11, body.Bytes())
}

func encStruct(fields ...structField) []byte {
	var body bytes.Buffer
	for _, field := range fields {
		body.Write(encVarUInt(field.nameSID))
		body.Write(field.value)
	}
	return encTLV(13, body.Bytes())
}

type structField struct {
	nameSID int
	value   []byte
}

func encAnnotation(annotSIDs []int, value []byte) []byte {
	var annotBody bytes.Buffer
	for _, sid := range annotSIDs {
		annotBody.Write(encVarUInt(sid))
	}
	var body bytes.Buffer
	body.Write(encVarUInt(annotBody.Len()))
	body.Write(annotBody.Bytes())
	body.Write(value)
	return encTLV(14, body.Bytes())
}

// trustedLocalSymbolTable builds:
//
//	$ion_symbol_table::{ symbols: ["audit", "evil_role"] }
//
// assigning SIDs 10 and 11 to the two texts.
func trustedLocalSymbolTable() []byte {
	structValue := encStruct(
		structField{nameSID: 7, value: encList(encString("audit"), encString("evil_role"))},
	)
	return encAnnotation([]int{3}, structValue)
}

func TestBinary_TrustedSymbolTableAndAllowedAnnotation(t *testing.T) {
	trusted := trustedLocalSymbolTable()
	// audit:: evil_role
	user := encAnnotation([]int{10}, encSymbol(11))
	trustedWithIVM := append(append([]byte{}, binaryIVM...), trusted...)
	err := New([]string{"audit"}, TrustedSegment(trustedWithIVM), UserSegment(user)).Validate()
	if err != nil {
		t.Fatalf("trusted LST + allowed annotation must pass: %v", err)
	}
}

func TestBinary_SymbolTableInUserDataRejected(t *testing.T) {
	// Same LST bytes, but the whole stream is uploaded user data, so the
	// directive must be refused before it can rebind anything.
	user := append(append([]byte{}, binaryIVM...), trustedLocalSymbolTable()...)
	err := New([]string{"audit"}, UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestBinary_MidstreamRebindingAcrossBoundaryRejected(t *testing.T) {
	trusted := trustedLocalSymbolTable()
	legitValue := encAnnotation([]int{10}, encSymbol(11))
	attackerLST := trustedLocalSymbolTable()

	err := New(
		[]string{"audit"},
		TrustedSegment(append(append([]byte{}, binaryIVM...), trusted...)),
		UserSegment(append(legitValue, attackerLST...)),
	).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestBinary_EmbeddedIVMRejected(t *testing.T) {
	user := encAnnotation([]int{10}, encSymbol(11))
	trustedWithIVM := append(append([]byte{}, binaryIVM...), trustedLocalSymbolTable()...)
	payload := append(append([]byte{}, user...), binaryIVM...) // illegal second IVM
	payload = append(payload, user...)
	err := New([]string{"audit"}, TrustedSegment(trustedWithIVM), UserSegment(payload)).Validate()
	if kind := violationKind(t, err); kind != KindIVM {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindIVM, err)
	}
}

func TestBinary_AnnotationOutsideAllowListRejected(t *testing.T) {
	trusted := trustedLocalSymbolTable()
	// evil_role:: audit, using SID 11 as an annotation.
	user := encAnnotation([]int{11}, encSymbol(10))

	trustedWithIVM := append(append([]byte{}, binaryIVM...), trusted...)
	err := New([]string{"audit"}, TrustedSegment(trustedWithIVM), UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestBinary_UnresolvableAnnotationRejected(t *testing.T) {
	// SID 99 was never declared; it must not bypass the allow list.
	user := append(append([]byte{}, binaryIVM...), encAnnotation([]int{99}, encSymbol(10))...)
	err := New([]string{"audit"}, UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestBinary_NestedDirectiveInUserDataRejected(t *testing.T) {
	// A user struct whose field value is an annotated LST directive.
	directive := encAnnotation([]int{3}, encStruct(
		structField{nameSID: 7, value: encList(encString("evil_role"))},
	))
	outer := encStruct(structField{nameSID: 4, value: directive})
	payload := append(append([]byte{}, binaryIVM...), outer...)
	err := New([]string{"audit"}, UserSegment(payload)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestBinary_SymbolIDZeroAnnotationRejected(t *testing.T) {
	user := encAnnotation([]int{0}, encSymbol(10))
	payload := append(append([]byte{}, binaryIVM...), user...)
	err := New([]string{"audit"}, UserSegment(payload)).Validate()
	if kind := violationKind(t, err); kind != KindAnnotation {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindAnnotation, err)
	}
}

func TestBinary_AllowListDoesNotGrantDirectiveTrust(t *testing.T) {
	user := append(append([]byte{}, binaryIVM...), trustedLocalSymbolTable()...)
	err := New([]string{annSymbolTable}, UserSegment(user)).Validate()
	if kind := violationKind(t, err); kind != KindSymbolTable {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindSymbolTable, err)
	}
}

func TestBinary_StreamMustStartWithIVM(t *testing.T) {
	stream := []byte{0xE0, 0x01, 0x00, 0x00, 0x71, 0x0A} // bad IVM trailing byte
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindMalformed {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindMalformed, err)
	}
}

func TestBinary_TruncatedValueRejected(t *testing.T) {
	stream := append(append([]byte{}, binaryIVM...), byte(0x83), 0x01)
	err := New(nil, UserSegment(stream)).Validate()
	if kind := violationKind(t, err); kind != KindMalformed {
		t.Fatalf("got kind %q, want %q (err=%v)", kind, KindMalformed, err)
	}
}

func TestIsViolation(t *testing.T) {
	if IsViolation(nil) {
		t.Fatal("nil must not be a violation")
	}
	if !IsViolation(Validate([]byte(`evil:: 1`), false, nil)) {
		t.Fatal("policy error must be recognized as a Violation")
	}
}

func TestEmptyStreamAccepted(t *testing.T) {
	if err := New(nil).Validate(); err != nil {
		t.Fatalf("empty stream must be accepted: %v", err)
	}
}
