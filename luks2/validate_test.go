package luks2

import (
	"errors"
	"strings"
	"testing"
)

func TestParseHeaderAcceptsValidMetadata(t *testing.T) {
	raw := buildRawHeader(t, validJSON(), testHeaderSize)
	limits := KDFLimits{MaxTime: 10, MaxMemory: 4194304, MaxCPUs: 16, MaxIterations: 100000}

	binaryHeader, header, err := ParseHeader(raw, limits)
	if err != nil {
		t.Fatalf("ParseHeader returned unexpected error: %v", err)
	}
	if binaryHeader.HeaderSize != testHeaderSize {
		t.Fatalf("header size = %d, want %d", binaryHeader.HeaderSize, testHeaderSize)
	}
	if len(header.Keyslots) != 2 || len(header.Digests) != 1 || len(header.Segments) != 1 {
		t.Fatalf("unexpected metadata counts: %+v", header)
	}
}

func TestRejectDuplicateKeys(t *testing.T) {
	cases := map[string]string{
		"top level":              `{"keyslots": {}, "keyslots": {}}`,
		"nested keyslot":         `{"keyslots": {"0": {"type": "luks2", "type": "retained"}}}`,
		"object nested in array": `{"digests": {"0": {"segments": [{"id": "0", "id": "1"}]}}}`,
	}
	for name, jsonText := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMetadata([]byte(jsonText))
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("error = %v, want ErrDuplicateKey", err)
			}
		})
	}
}

func TestRejectMalformedJSON(t *testing.T) {
	for _, jsonText := range []string{
		"",
		"\x00\x00",
		`{"keyslots":`,
		`{"keyslots": {}} extra`,
	} {
		if _, err := ParseMetadata([]byte(jsonText)); !errors.Is(err, ErrMalformedJSON) {
			t.Fatalf("input %q: error = %v, want ErrMalformedJSON", jsonText, err)
		}
	}
}

func TestValidateAreasOverlapAndBounds(t *testing.T) {
	limits := KDFLimits{MaxTime: 100, MaxMemory: 1 << 30, MaxCPUs: 64}

	t.Run("overlapping areas rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"offset": "163840", "size": "131072"`,
			`"offset": "100000", "size": "131072"`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateAreas(header, testHeaderSize); !errors.Is(err, ErrAreaOverlap) {
			t.Fatalf("error = %v, want ErrAreaOverlap", err)
		}
	})

	t.Run("area before keyslots region rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"offset": "32768", "size": "131072"`,
			`"offset": "4096", "size": "131072"`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateAreas(header, testHeaderSize); !errors.Is(err, ErrAreaOutOfBounds) {
			t.Fatalf("error = %v, want ErrAreaOutOfBounds", err)
		}
	})

	t.Run("area reaching into segment rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"offset": "163840", "size": "131072"`,
			`"offset": "8300000", "size": "131072"`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateAreas(header, testHeaderSize); !errors.Is(err, ErrAreaOutOfBounds) {
			t.Fatalf("error = %v, want ErrAreaOutOfBounds", err)
		}
	})

	t.Run("adjacent areas are allowed", func(t *testing.T) {
		header := parseValid(t, validJSON())
		if err := Validate(header, testHeaderSize, limits); err != nil {
			t.Fatalf("adjacent areas unexpectedly rejected: %v", err)
		}
	})

	t.Run("zero size area rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"offset": "32768", "size": "131072"`,
			`"offset": "32768", "size": "0"`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateAreas(header, testHeaderSize); !errors.Is(err, ErrMalformedMetadata) {
			t.Fatalf("error = %v, want ErrMalformedMetadata", err)
		}
	})
}

func TestValidateBindingsDanglingAndReverse(t *testing.T) {
	t.Run("dangling keyslot reference rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"keyslots": ["0", "1"]`,
			`"keyslots": ["0", "9"]`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateBindings(header); !errors.Is(err, ErrDanglingReference) {
			t.Fatalf("error = %v, want ErrDanglingReference", err)
		}
	})

	t.Run("dangling segment reference rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"segments": ["0"]`,
			`"segments": ["7"]`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateBindings(header); !errors.Is(err, ErrDanglingReference) {
			t.Fatalf("error = %v, want ErrDanglingReference", err)
		}
	})

	t.Run("unbound keyslot rejected", func(t *testing.T) {
		jsonText := strings.Replace(validJSON(),
			`"keyslots": ["0", "1"]`,
			`"keyslots": ["0"]`, 1)
		header := parseValid(t, jsonText)
		if err := ValidateBindings(header); !errors.Is(err, ErrBindingInconsistent) {
			t.Fatalf("error = %v, want ErrBindingInconsistent", err)
		}
	})

	t.Run("unbound segment rejected", func(t *testing.T) {
		header := parseValid(t, validJSON())
		header.Segments["1"] = Segment{Type: "crypt", Offset: uint64Field(16777216)}
		if err := ValidateBindings(header); !errors.Is(err, ErrBindingInconsistent) {
			t.Fatalf("error = %v, want ErrBindingInconsistent", err)
		}
	})

	t.Run("retained keyslot needs no reverse binding", func(t *testing.T) {
		header := parseValid(t, validJSON())
		header.Keyslots["2"] = Keyslot{Type: TypeRetained}
		if err := ValidateBindings(header); err != nil {
			t.Fatalf("retained keyslot unexpectedly required a binding: %v", err)
		}
	})

	t.Run("reference to retained keyslot rejected", func(t *testing.T) {
		header := parseValid(t, validJSON())
		header.Keyslots["2"] = Keyslot{Type: TypeRetained}
		digest := header.Digests["0"]
		digest.Keyslots = append(digest.Keyslots, "2")
		header.Digests["0"] = digest
		if err := ValidateBindings(header); !errors.Is(err, ErrDanglingReference) {
			t.Fatalf("error = %v, want ErrDanglingReference", err)
		}
	})
}

func TestValidateKDFLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits KDFLimits
	}{
		{"time", KDFLimits{MaxTime: 3}},
		{"memory", KDFLimits{MaxMemory: 524288}},
		{"cpus", KDFLimits{MaxCPUs: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := parseValid(t, validJSON())
			if err := ValidateKDF(header, tc.limits); !errors.Is(err, ErrKDFLimit) {
				t.Fatalf("error = %v, want ErrKDFLimit", err)
			}
		})
	}

	t.Run("zero limit means unlimited", func(t *testing.T) {
		header := parseValid(t, validJSON())
		if err := ValidateKDF(header, KDFLimits{}); err != nil {
			t.Fatalf("zero limits should not reject anything, got %v", err)
		}
	})

	t.Run("pbkdf2 iterations capped", func(t *testing.T) {
		header := parseValid(t, validJSON())
		keyslot := header.Keyslots["0"]
		keyslot.KDF = &KDF{Type: "pbkdf2", Iterations: 500000, Hash: "sha256", Salt: "c2FsdA=="}
		header.Keyslots["0"] = keyslot
		err := ValidateKDF(header, KDFLimits{MaxIterations: 100000})
		if !errors.Is(err, ErrKDFLimit) {
			t.Fatalf("error = %v, want ErrKDFLimit", err)
		}
	})

	t.Run("boundary value accepted", func(t *testing.T) {
		header := parseValid(t, validJSON())
		err := ValidateKDF(header, KDFLimits{MaxTime: 5, MaxMemory: 2097152, MaxCPUs: 8})
		if err != nil {
			t.Fatalf("values equal to limits should pass, got %v", err)
		}
	})
}
