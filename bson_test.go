package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func makeDoc(elements ...[]byte) []byte {
	total := 4 + 1
	for _, element := range elements {
		total += len(element)
	}
	out := make([]byte, 4, total)
	binary.LittleEndian.PutUint32(out, uint32(total))
	for _, element := range elements {
		out = append(out, element...)
	}
	return append(out, 0x00)
}

func makeElement(typ byte, name string, value []byte) []byte {
	out := make([]byte, 0, 1+len(name)+1+len(value))
	out = append(out, typ)
	out = append(out, name...)
	out = append(out, 0x00)
	return append(out, value...)
}

func makeString(value string) []byte {
	out := make([]byte, 4, 4+len(value)+1)
	binary.LittleEndian.PutUint32(out, uint32(len(value)+1))
	out = append(out, value...)
	return append(out, 0x00)
}

func makeInt32(value int32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(value))
	return out
}

func makeInt64(value int64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, uint64(value))
	return out
}

func makeFixed(n int) []byte {
	return make([]byte, n)
}

func makeCString(value string) []byte {
	return append([]byte(value), 0x00)
}

func makeCodeWithScope(code string, scope []byte) []byte {
	codeBytes := makeString(code)
	total := 4 + len(codeBytes) + len(scope)
	out := make([]byte, 4, total)
	binary.LittleEndian.PutUint32(out, uint32(total))
	out = append(out, codeBytes...)
	return append(out, scope...)
}

func makeArray(values ...[]byte) []byte {
	elements := make([][]byte, 0, len(values))
	for i, value := range values {
		elements = append(elements, makeElement(bsonString, string(rune('0'+i)), value))
	}
	return makeDoc(elements...)
}

func validDocument() []byte {
	profile := makeDoc(
		makeElement(bsonString, "region", makeString("cn-east")),
		makeElement(bsonInt32, "level", makeInt32(7)),
		makeElement(bsonArray, "tags", makeArray(makeString("prod"), makeString("gateway"))),
	)
	return makeDoc(
		makeElement(bsonString, "tenant", makeString("acme")),
		makeElement(bsonDocument, "profile", profile),
		makeElement(bsonDouble, "score", makeFixed(8)),
	)
}

func TestValidateAcceptsLegalDocument(t *testing.T) {
	if err := Validate(validDocument()); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
}

func TestValidateAcceptsEveryScalarType(t *testing.T) {
	empty := makeDoc()
	doc := makeDoc(
		makeElement(bsonDouble, "double", makeFixed(8)),
		makeElement(bsonString, "string", makeString("value")),
		makeElement(bsonDocument, "document", empty),
		makeElement(bsonArray, "array", makeArray(makeString("one"), makeString("two"))),
		makeElement(bsonBinary, "binary", append(makeInt32(3), append([]byte{0x00}, []byte{1, 2, 3}...)...)),
		makeElement(bsonUndefined, "undefined", nil),
		makeElement(bsonObjectID, "objectId", makeFixed(12)),
		makeElement(bsonBoolean, "boolean", []byte{1}),
		makeElement(bsonDateTime, "dateTime", makeFixed(8)),
		makeElement(bsonNull, "null", nil),
		makeElement(bsonRegex, "regex", append(makeCString("^a"), makeCString("i")...)),
		makeElement(bsonDBPointer, "dbPointer", append(makeString("db.collection"), makeFixed(12)...)),
		makeElement(bsonJavaScript, "javascript", makeString("return 1")),
		makeElement(bsonSymbol, "symbol", makeString("sym")),
		makeElement(bsonCodeWithScope, "codeWithScope", makeCodeWithScope("return 1", empty)),
		makeElement(bsonInt32, "int32", makeInt32(-1)),
		makeElement(bsonTimestamp, "timestamp", makeFixed(8)),
		makeElement(bsonInt64, "int64", makeInt64(-1)),
		makeElement(bsonDecimal128, "decimal128", makeFixed(16)),
		makeElement(bsonMaxKey, "maxKey", nil),
		makeElement(bsonMinKey, "minKey", nil),
	)
	if err := Validate(doc); err != nil {
		t.Fatalf("legal document with all scalar types rejected: %v", err)
	}
}

func TestValidateRejectsTotalLengthMismatch(t *testing.T) {
	valid := validDocument()

	t.Run("trailing bytes", func(t *testing.T) {
		forged := append(append([]byte(nil), valid...), 0xAA)
		err := Validate(forged)
		if !errors.Is(err, ErrTotalLength) {
			t.Fatalf("got %v, want ErrTotalLength", err)
		}
	})

	t.Run("declared longer than buffer", func(t *testing.T) {
		forged := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(forged[0:4], uint32(len(valid)+7))
		err := Validate(forged)
		if !errors.Is(err, ErrTotalLength) {
			t.Fatalf("got %v, want ErrTotalLength", err)
		}
	})

	t.Run("declared shorter than buffer", func(t *testing.T) {
		forged := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(forged[0:4], uint32(len(valid)-1))
		err := Validate(forged)
		if !errors.Is(err, ErrTotalLength) {
			t.Fatalf("got %v, want ErrTotalLength", err)
		}
	})
}

func TestValidateRejectsNestedDocumentCrossingParent(t *testing.T) {
	innerActual := makeDoc(makeElement(bsonString, "tenant", makeString("nested")))
	innerForged := append([]byte(nil), innerActual...)
	binary.LittleEndian.PutUint32(innerForged[0:4], uint32(len(innerActual)+6))

	outer := makeDoc(makeElement(bsonDocument, "outer", innerForged))
	root := makeDoc(
		makeElement(bsonDocument, "wrapped", outer),
		makeElement(bsonInt32, "padding", makeInt32(1)),
	)

	err := Validate(root)
	if !errors.Is(err, ErrNestedOverflow) {
		t.Fatalf("got %v, want ErrNestedOverflow", err)
	}
}

func TestValidateRejectsNestedDocumentWithShortLength(t *testing.T) {
	innerActual := makeDoc(makeElement(bsonInt32, "n", makeInt32(0x01020304)))
	innerForged := append([]byte(nil), innerActual...)
	binary.LittleEndian.PutUint32(innerForged[0:4], uint32(len(innerActual)-1))

	root := makeDoc(makeElement(bsonDocument, "inner", innerForged))
	err := Validate(root)
	if !errors.Is(err, ErrEarlyTerminator) {
		t.Fatalf("got %v, want ErrEarlyTerminator", err)
	}
}

func TestValidateRejectsUnterminatedCString(t *testing.T) {
	// The element name "tenant" has no NUL before the document terminator.
	element := []byte{bsonInt32}
	element = append(element, []byte("tenant")...)
	element = append(element, makeInt32(0x01020304)...)
	doc := makeDoc(element)

	err := Validate(doc)
	if !errors.Is(err, ErrCString) {
		t.Fatalf("got %v, want ErrCString", err)
	}
}

func TestValidateRejectsStringLengthExcludingNUL(t *testing.T) {
	// Declares 3 bytes for "abc\0"; the terminator lies outside the length.
	value := make([]byte, 4, 8)
	binary.LittleEndian.PutUint32(value, 3)
	value = append(value, []byte("abc")...)
	value = append(value, 0x00)

	err := Validate(makeDoc(makeElement(bsonString, "tenant", value)))
	if !errors.Is(err, ErrStringLength) {
		t.Fatalf("got %v, want ErrStringLength", err)
	}
}

func TestValidateRejectsDuplicateTenantKey(t *testing.T) {
	doc := makeDoc(
		makeElement(bsonString, "tenant", makeString("aaa")),
		makeElement(bsonString, "tenant", makeString("bbb")),
	)

	err := Validate(doc)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("got %v, want ErrDuplicateKey", err)
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type %T is not *ValidationError", err)
	}
	if validationErr.Path != "tenant" {
		t.Fatalf("path = %q, want tenant", validationErr.Path)
	}
}

func TestValidateAllowsSameKeyAtDifferentLevels(t *testing.T) {
	nested := makeDoc(makeElement(bsonString, "tenant", makeString("nested")))
	doc := makeDoc(
		makeElement(bsonString, "tenant", makeString("root")),
		makeElement(bsonDocument, "profile", nested),
	)

	if err := Validate(doc); err != nil {
		t.Fatalf("same key at different levels rejected: %v", err)
	}
}

func TestValidateRejectsDuplicateKeyInsideNestedDocument(t *testing.T) {
	nested := makeDoc(
		makeElement(bsonString, "tenant", makeString("a")),
		makeElement(bsonString, "tenant", makeString("b")),
	)
	err := Validate(makeDoc(makeElement(bsonDocument, "profile", nested)))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("got %v, want ErrDuplicateKey", err)
	}
}

func TestValidateRejectsDuplicateArrayIndexKey(t *testing.T) {
	array := makeDoc(
		makeElement(bsonString, "0", makeString("first")),
		makeElement(bsonString, "0", makeString("second")),
	)
	err := Validate(makeDoc(makeElement(bsonArray, "items", array)))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("got %v, want ErrDuplicateKey", err)
	}
}

func TestValidateKeyPolicy(t *testing.T) {
	dollarDoc := makeDoc(makeElement(bsonString, "$set", makeString("x")))
	dottedDoc := makeDoc(makeElement(bsonString, "user.name", makeString("x")))
	bothDoc := makeDoc(
		makeElement(bsonString, "$set", makeString("x")),
		makeElement(bsonString, "user.name", makeString("y")),
	)

	t.Run("strict zero policy rejects dollar", func(t *testing.T) {
		if err := Validate(dollarDoc); !errors.Is(err, ErrDollarKey) {
			t.Fatalf("got %v, want ErrDollarKey", err)
		}
	})
	t.Run("strict zero policy rejects dots", func(t *testing.T) {
		if err := Validate(dottedDoc); !errors.Is(err, ErrDottedKey) {
			t.Fatalf("got %v, want ErrDottedKey", err)
		}
	})
	t.Run("caller can allow dollar keys", func(t *testing.T) {
		if err := ValidateWithPolicy(dollarDoc, Policy{AllowDollarKeys: true}); err != nil {
			t.Fatalf("allowed dollar key rejected: %v", err)
		}
	})
	t.Run("caller can allow dotted keys", func(t *testing.T) {
		if err := ValidateWithPolicy(dottedDoc, Policy{AllowDottedKeys: true}); err != nil {
			t.Fatalf("allowed dotted key rejected: %v", err)
		}
	})
	t.Run("caller can allow both", func(t *testing.T) {
		if err := ValidateWithPolicy(bothDoc, Policy{AllowDollarKeys: true, AllowDottedKeys: true}); err != nil {
			t.Fatalf("allowed keys rejected: %v", err)
		}
	})
}

func TestValidateRejectsEarlyTerminatorAndTruncatedValue(t *testing.T) {
	t.Run("early terminator", func(t *testing.T) {
		doc := []byte{10, 0, 0, 0, 0x00, 1, 2, 3, 4, 0}
		err := Validate(doc)
		if !errors.Is(err, ErrEarlyTerminator) {
			t.Fatalf("got %v, want ErrEarlyTerminator", err)
		}
	})
	t.Run("truncated fixed-width value", func(t *testing.T) {
		doc := makeDoc(makeElement(bsonInt32, "x", []byte{1, 2}))
		err := Validate(doc)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("got %v, want ErrTruncated", err)
		}
	})
	t.Run("unknown type", func(t *testing.T) {
		doc := makeDoc(makeElement(0x42, "x", nil))
		err := Validate(doc)
		if !errors.Is(err, ErrUnsupportedType) {
			t.Fatalf("got %v, want ErrUnsupportedType", err)
		}
	})
}

func TestValidateRespectsMaxDepth(t *testing.T) {
	doc := makeDoc(makeElement(bsonString, "leaf", makeString("x")))
	for i := 0; i < 5; i++ {
		doc = makeDoc(makeElement(bsonDocument, "child", doc))
	}

	if err := ValidateWithPolicy(doc, Policy{MaxDepth: 5}); err != nil {
		t.Fatalf("document at allowed depth rejected: %v", err)
	}
	err := ValidateWithPolicy(doc, Policy{MaxDepth: 4})
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("got %v, want ErrDepthExceeded", err)
	}
}

func TestValidationErrorOffsetPointsAtBadByte(t *testing.T) {
	valid := validDocument()
	forged := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(forged[0:4], uint32(len(valid)+2))

	err := Validate(forged)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type %T is not *ValidationError", err)
	}
	if validationErr.Offset != 0 || !strings.Contains(validationErr.Error(), "declared") {
		t.Fatalf("unexpected error detail: %v", validationErr)
	}
}

func TestValidateDoesNotAliasInput(t *testing.T) {
	valid := validDocument()
	original := append([]byte(nil), valid...)
	if err := Validate(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if !bytes.Equal(valid, original) {
		t.Fatal("validator modified its input")
	}
}

func FuzzValidateDoesNotPanic(f *testing.F) {
	f.Add(validDocument())
	f.Add([]byte{})
	f.Add([]byte{5, 0})
	f.Add([]byte{5, 0, 0, 0, 0})
	f.Add([]byte{9, 0, 0, 0, 0x10, 'x', 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = Validate(data)
		_ = ValidateWithPolicy(data, Policy{AllowDollarKeys: true, AllowDottedKeys: true, MaxDepth: 16})
	})
}
