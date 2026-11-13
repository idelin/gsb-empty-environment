package netsec

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func known() map[string]bool {
	return map[string]bool{"http://example.com/known": true}
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestValidateHeaders(t *testing.T) {
	tests := []struct {
		name        string
		protected   map[string]any
		unprotected map[string]any
		known       map[string]bool
		wantErr     error
	}{
		{
			name:      "no crit is accepted",
			protected: map[string]any{"alg": "HS256"},
			known:     known(),
			wantErr:   nil,
		},
		{
			name: "known critical extension present in protected header is accepted",
			protected: map[string]any{
				"alg":                      "HS256",
				"crit":                     []any{"http://example.com/known"},
				"http://example.com/known": 42,
			},
			known:   known(),
			wantErr: nil,
		},
		{
			name: "crit written in both headers is rejected",
			protected: map[string]any{
				"alg":                      "HS256",
				"crit":                     []any{"http://example.com/known"},
				"http://example.com/known": 42,
			},
			unprotected: map[string]any{
				"crit":                     []any{"http://example.com/known"},
				"http://example.com/known": 42,
			},
			known:   known(),
			wantErr: ErrCritInUnprotected,
		},
		{
			name: "crit only in unprotected header is rejected even when extension is known",
			protected: map[string]any{
				"alg":                      "HS256",
				"http://example.com/known": 42,
			},
			unprotected: map[string]any{
				"crit": []any{"http://example.com/known"},
			},
			known:   known(),
			wantErr: ErrCritInUnprotected,
		},
		{
			name: "unknown extension listed in protected crit is rejected",
			protected: map[string]any{
				"alg":                        "HS256",
				"crit":                       []any{"http://example.com/unknown"},
				"http://example.com/unknown": true,
			},
			known:   known(),
			wantErr: ErrCritExtensionUnknown,
		},
		{
			name: "unknown extension placed only in unprotected crit is rejected",
			protected: map[string]any{
				"alg":                        "HS256",
				"crit":                       []any{"http://example.com/unknown"},
				"http://example.com/unknown": true,
			},
			unprotected: map[string]any{
				"crit": []any{"http://example.com/unknown"},
			},
			known:   known(),
			wantErr: ErrCritInUnprotected,
		},
		{
			name: "critical extension missing from protected header is rejected",
			protected: map[string]any{
				"alg":  "HS256",
				"crit": []any{"http://example.com/known"},
			},
			known:   known(),
			wantErr: ErrCritExtensionMissing,
		},
		{
			name: "critical extension carried only in unprotected header is rejected",
			protected: map[string]any{
				"alg":  "HS256",
				"crit": []any{"http://example.com/known"},
			},
			unprotected: map[string]any{
				"http://example.com/known": 42,
			},
			known:   known(),
			wantErr: ErrCritExtensionUnprotected,
		},
		{
			name: "crit that is not an array is rejected",
			protected: map[string]any{
				"alg":  "HS256",
				"crit": "http://example.com/known",
			},
			known:   known(),
			wantErr: ErrCritNotArray,
		},
		{
			name: "empty crit array is rejected",
			protected: map[string]any{
				"alg":  "HS256",
				"crit": []any{},
			},
			known:   known(),
			wantErr: ErrCritEmpty,
		},
		{
			name: "non-string crit entry is rejected",
			protected: map[string]any{
				"alg":                      "HS256",
				"crit":                     []any{42},
				"http://example.com/known": 1,
			},
			known:   known(),
			wantErr: ErrCritEntryNotString,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHeaders(tt.protected, tt.unprotected, tt.known)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateHeaders() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func compactJWS(t *testing.T, protected map[string]any) string {
	t.Helper()
	header := encodeJSON(t, protected)
	return header + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("signature"))
}

func TestValidateCompact(t *testing.T) {
	validProtected := map[string]any{
		"alg":                      "HS256",
		"crit":                     []any{"http://example.com/known"},
		"http://example.com/known": 42,
	}

	t.Run("known critical extension in compact token is accepted", func(t *testing.T) {
		if err := ValidateCompact(compactJWS(t, validProtected), known()); err != nil {
			t.Fatalf("ValidateCompact() unexpected error: %v", err)
		}
	})

	t.Run("unknown critical extension in compact token is rejected", func(t *testing.T) {
		protected := map[string]any{
			"alg":                        "HS256",
			"crit":                       []any{"http://example.com/unknown"},
			"http://example.com/unknown": true,
		}
		err := ValidateCompact(compactJWS(t, protected), known())
		if !errors.Is(err, ErrCritExtensionUnknown) {
			t.Fatalf("ValidateCompact() error = %v, want %v", err, ErrCritExtensionUnknown)
		}
	})

	t.Run("malformed compact token is rejected", func(t *testing.T) {
		if err := ValidateCompact("a.b", known()); err == nil {
			t.Fatal("ValidateCompact() expected error for malformed token, got nil")
		}
	})

	t.Run("undecodable protected header is rejected", func(t *testing.T) {
		if err := ValidateCompact("@@@.e30.sig", known()); err == nil {
			t.Fatal("ValidateCompact() expected error for bad base64url, got nil")
		}
	})
}
