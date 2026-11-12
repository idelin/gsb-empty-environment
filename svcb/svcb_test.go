package svcb

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func wireName(name string) []byte {
	if name == "." {
		return []byte{0}
	}
	var out []byte
	for _, label := range strings.Split(name, ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func wireParam(key uint16, value []byte) []byte {
	out := append(be16(key), be16(uint16(len(value)))...)
	return append(out, value...)
}

func wireRDATA(priority uint16, target string, params ...[]byte) []byte {
	out := append(be16(priority), wireName(target)...)
	for _, param := range params {
		out = append(out, param...)
	}
	return out
}

func fullValidParams() [][]byte {
	mandatory := append(be16(KeyALPN), be16(KeyECH)...)
	return [][]byte{
		wireParam(KeyMandatory, mandatory),
		wireParam(KeyALPN, []byte{2, 'h', '2'}),
		wireParam(KeyNoDefaultALPN, nil),
		wireParam(KeyPort, be16(8443)),
		wireParam(KeyIPv4Hint, []byte{192, 0, 2, 1, 192, 0, 2, 2}),
		wireParam(KeyECH, []byte{0x00, 0x01, 0x02, 0x03}),
		wireParam(KeyIPv6Hint, make([]byte, 32)),
		wireParam(KeyDOHPath, []byte("/dns-query")),
	}
}

func TestParseAndValidateValid(t *testing.T) {
	rdata := wireRDATA(1, "example.com", fullValidParams()...)

	record, err := ParseAndValidate(rdata)
	if err != nil {
		t.Fatalf("expected valid record, got %v", err)
	}
	if record.SvcPriority != 1 || record.TargetName != "example.com" {
		t.Fatalf("unexpected header: priority=%d target=%q", record.SvcPriority, record.TargetName)
	}
	if len(record.Params) != 8 {
		t.Fatalf("expected 8 params, got %d", len(record.Params))
	}
	if record.Params[0].Key != KeyMandatory || len(record.Params[0].Value) != 4 {
		t.Fatalf("unexpected mandatory param: %+v", record.Params[0])
	}
}

func TestRootTargetName(t *testing.T) {
	record, err := ParseAndValidate(wireRDATA(1, ".", wireParam(KeyPort, be16(443))))
	if err != nil {
		t.Fatalf("root target name failed: %v", err)
	}
	if record.TargetName != "." {
		t.Fatalf("expected root name \".\", got %q", record.TargetName)
	}
}

func TestAliasMode(t *testing.T) {
	t.Run("no params accepted", func(t *testing.T) {
		if _, err := ParseAndValidate(wireRDATA(0, "alias.example.com")); err != nil {
			t.Fatalf("alias mode without params must be valid, got %v", err)
		}
	})

	t.Run("params rejected", func(t *testing.T) {
		rdata := wireRDATA(0, "alias.example.com", wireParam(KeyPort, be16(443)))
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrAliasModeParams, KeyPort)
	})
}

func TestKeyOrderingViolations(t *testing.T) {
	t.Run("not strictly ascending", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyECH, []byte{1}),
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrKeysNotAscending, KeyALPN)
	})

	t.Run("duplicate key", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
			wireParam(KeyALPN, []byte{2, 'h', '3'}),
			wireParam(KeyECH, []byte{1}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrDuplicateKey, KeyALPN)
	})
}

func TestMandatoryViolations(t *testing.T) {
	t.Run("list not ascending", func(t *testing.T) {
		value := append(be16(KeyECH), be16(KeyALPN)...)
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, value),
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
			wireParam(KeyECH, []byte{1}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMandatoryNotAscending, KeyALPN)
	})

	t.Run("duplicate entry in list", func(t *testing.T) {
		value := append(be16(KeyALPN), be16(KeyALPN)...)
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, value),
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMandatoryNotAscending, KeyALPN)
	})

	t.Run("contains key zero itself", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyMandatory, be16(0)))
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMandatorySelf, 0)
	})

	t.Run("references missing key", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, be16(KeyECH)),
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMandatoryMissingKey, KeyECH)
	})

	t.Run("references unsupported key", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, be16(KeyECH)),
			wireParam(KeyECH, []byte{1}),
		)
		_, err := ParseAndValidateWith(rdata, SupportedKeys(KeyMandatory, KeyECH))
		if err != nil {
			t.Fatalf("ech is in the supported set, got %v", err)
		}

		_, err = ParseAndValidateWith(rdata, SupportedKeys(KeyMandatory))
		assertViolation(t, err, ErrMandatoryUnsupported, KeyECH)
	})

	t.Run("odd-length value", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, []byte{0, 1, 2}),
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMalformedValue, KeyMandatory)
	})
}

func TestNoDefaultALPNViolations(t *testing.T) {
	t.Run("non-empty value", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyALPN, []byte{2, 'h', '2'}),
			wireParam(KeyNoDefaultALPN, []byte{0xAA}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrNoDefaultALPNValue, KeyNoDefaultALPN)
	})

	t.Run("missing alpn companion", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyNoDefaultALPN, nil))
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrNoDefaultALPNNoALPN, KeyNoDefaultALPN)
	})
}

func TestHintLengthViolations(t *testing.T) {
	t.Run("ipv4hint not multiple of four", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyIPv4Hint, []byte{1, 2, 3}))
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrIPv4HintLength, KeyIPv4Hint)
	})

	t.Run("ipv4hint multiple of four accepted", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyIPv4Hint, make([]byte, 8)))
		if _, err := ParseAndValidate(rdata); err != nil {
			t.Fatalf("8-byte ipv4hint must be valid, got %v", err)
		}
	})

	t.Run("ipv6hint not multiple of sixteen", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyIPv6Hint, make([]byte, 17)))
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrIPv6HintLength, KeyIPv6Hint)
	})

	t.Run("ipv6hint multiple of sixteen accepted", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com", wireParam(KeyIPv6Hint, make([]byte, 32)))
		if _, err := ParseAndValidate(rdata); err != nil {
			t.Fatalf("32-byte ipv6hint must be valid, got %v", err)
		}
	})
}

func TestUnknownKeys(t *testing.T) {
	t.Run("unknown non-mandatory key tolerated", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(99, []byte{1, 2, 3}),
			wireParam(100, nil),
		)
		if _, err := ParseAndValidate(rdata); err != nil {
			t.Fatalf("unknown non-mandatory keys must be tolerated, got %v", err)
		}
	})

	t.Run("unknown mandatory key unsupported", func(t *testing.T) {
		rdata := wireRDATA(1, "example.com",
			wireParam(KeyMandatory, be16(99)),
			wireParam(99, []byte{1}),
		)
		_, err := ParseAndValidate(rdata)
		assertViolation(t, err, ErrMandatoryUnsupported, 99)
	})
}

func TestParseTruncation(t *testing.T) {
	fullName := wireName("example.com")
	paramPrefix := func(key uint16, length uint16) []byte {
		return append(be16(key), be16(length)...)
	}
	cases := map[string][]byte{
		"empty":                 nil,
		"half priority":         {0x00},
		"priority without name": be16(1),
		"name cut in label":     append(be16(1), 3, 'a', 'b'),
		"param key only":        append(append(be16(1), fullName...), be16(1)...),
		"param length no value": append(
			append(be16(1), fullName...),
			paramPrefix(KeyPort, 4)...,
		),
		"param value truncated": append(
			append(be16(1), fullName...),
			append(paramPrefix(KeyPort, 4), 0, 0)...,
		),
	}
	for name, rdata := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(rdata); !errors.Is(err, ErrTruncated) {
				t.Fatalf("expected ErrTruncated, got %v", err)
			}
		})
	}
}

func TestCompressedNameRejected(t *testing.T) {
	rdata := append(be16(1), 0xC0, 0x00)
	if _, err := Parse(rdata); !errors.Is(err, ErrCompressedName) {
		t.Fatalf("expected ErrCompressedName, got %v", err)
	}
}

func TestOversizedNameRejected(t *testing.T) {
	var name []byte
	for i := 0; i < 5; i++ {
		name = append(name, 60)
		name = append(name, make([]byte, 60)...)
	}
	name = append(name, 0)
	rdata := append(be16(1), name...)
	if _, err := Parse(rdata); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestParsedValuesDoNotAliasInput(t *testing.T) {
	rdata := wireRDATA(1, "example.com", wireParam(KeyECH, []byte{1, 2, 3, 4}))
	record, err := Parse(rdata)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	for i := range rdata {
		rdata[i] = 0xFF
	}
	ech := record.Params[0].Value
	if len(ech) != 4 || ech[0] != 1 || ech[3] != 4 {
		t.Fatalf("parsed value aliases input or corrupted: %v", ech)
	}
}

func TestDefaultSupportedKeysIsCopy(t *testing.T) {
	set := DefaultSupportedKeys()
	set[KeyECH] = false

	rdata := wireRDATA(1, "example.com",
		wireParam(KeyMandatory, be16(KeyECH)),
		wireParam(KeyECH, []byte{1}),
	)
	if _, err := ParseAndValidate(rdata); err != nil {
		t.Fatalf("default set must still include ech, got %v", err)
	}
}

func TestValidateEntryPoints(t *testing.T) {
	rdata := wireRDATA(1, "example.com", wireParam(KeyIPv4Hint, []byte{1, 2}))

	record, err := Parse(rdata)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if err := Validate(record); !errors.Is(err, ErrIPv4HintLength) {
		t.Fatalf("Validate expected ErrIPv4HintLength, got %v", err)
	}

	_, pavErr := ParseAndValidate(rdata)
	if !errors.Is(pavErr, ErrIPv4HintLength) {
		t.Fatalf("ParseAndValidate expected ErrIPv4HintLength, got %v", pavErr)
	}

	if _, err := ParseAndValidateWith(nil, nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("ParseAndValidateWith expected ErrTruncated, got %v", err)
	}

	var validationErr *Error
	if !errors.As(pavErr, &validationErr) {
		t.Fatalf("expected *svcb.Error, got %T", pavErr)
	}
	text := validationErr.Error()
	if !strings.Contains(text, "svcb:") || !strings.Contains(text, "ipv4hint") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func assertViolation(t *testing.T, err error, kind error, key uint16) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("expected %v, got %v", kind, err)
	}
	var validationErr *Error
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *svcb.Error, got %T", err)
	}
	if validationErr.Key != key {
		t.Fatalf("expected offending key %d, got %d", key, validationErr.Key)
	}
}
