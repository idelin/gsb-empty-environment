package strictjson

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUnmarshalRejectsDuplicateKeys(t *testing.T) {
	cases := map[string]string{
		"top level":     `{"alg":"HS256","alg":"none"}`,
		"nested object": `{"header":{"alg":"HS256","alg":"none"}}`,
		"inside array":  `[{"kid":"a","kid":"b"}]`,
		"mixed fields":  `{"iss":"a","sub":"b","iss":"c"}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var v map[string]any
			err := Unmarshal([]byte(input), &v)
			if err == nil {
				t.Fatalf("expected error for %s, got value %v", input, v)
			}
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("expected ErrDuplicateKey, got %v", err)
			}
			var dup *DuplicateKeyError
			if !errors.As(err, &dup) {
				t.Fatalf("expected *DuplicateKeyError, got %v", err)
			}
		})
	}
}

func TestUnmarshalAcceptsUniqueKeys(t *testing.T) {
	input := `{"alg":"HS256","typ":"JWT","nested":{"kid":"key-1"},"arr":[1,2,3]}`
	var v map[string]any
	if err := Unmarshal([]byte(input), &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v["alg"] != "HS256" || v["typ"] != "JWT" {
		t.Fatalf("unexpected decoded value: %v", v)
	}
}

func TestUnmarshalRejectsTrailingData(t *testing.T) {
	var v map[string]any
	if err := Unmarshal([]byte(`{"a":1}{"b":2}`), &v); err == nil {
		t.Fatal("expected trailing-data error")
	}
}

func TestDecoderRejectsDuplicateKeys(t *testing.T) {
	var v map[string]any
	err := NewDecoder(strings.NewReader(`{"alg":"HS256","alg":"HS384"}`)).Decode(&v)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
}

func TestStandardLibraryOverwritesDuplicateKey(t *testing.T) {
	// Documents the ambiguity the strict decoder removes: encoding/json keeps
	// the last occurrence, which would let the second "alg" win.
	var v map[string]any
	if err := json.Unmarshal([]byte(`{"alg":"HS256","alg":"none"}`), &v); err != nil {
		t.Fatalf("standard unmarshal error: %v", err)
	}
	if v["alg"] != "none" {
		t.Fatalf("standard library should keep last value, got %v", v["alg"])
	}
}
