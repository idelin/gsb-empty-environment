package netsec

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func mustObjectDisclosure(t *testing.T, salt, name string, value any) (string, string) {
	t.Helper()
	raw, err := CreateDisclosure(salt, name, value)
	if err != nil {
		t.Fatalf("CreateDisclosure: %v", err)
	}
	return raw, DisclosureDigest(raw)
}

func mustArrayDisclosure(t *testing.T, salt string, value any) (string, string) {
	t.Helper()
	raw, err := CreateArrayDisclosure(salt, value)
	if err != nil {
		t.Fatalf("CreateArrayDisclosure: %v", err)
	}
	return raw, DisclosureDigest(raw)
}

func TestVerifySingleGenuineDisclosure(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-1", "given_name", "Alice")
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"_sd":     []any{digest},
		"iss":     "https://issuer.example",
	}

	claims, err := Verify(payload, []string{raw})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := claims["given_name"]; got != "Alice" {
		t.Fatalf("given_name = %v, want Alice", got)
	}
	if got := claims["_sd"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("_sd = %#v, want empty array", got)
	}
	if _, exists := payload["given_name"]; exists {
		t.Fatal("Verify mutated the caller's payload")
	}
}

func TestVerifyOnlyAcceptsSHA256(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-1", "given_name", "Alice")
	for _, algorithm := range []any{"sha-512", "SHA-256", "sha256", 256, nil} {
		payload := map[string]any{
			"_sd_alg": algorithm,
			"_sd":     []any{digest},
		}
		_, err := Verify(payload, []string{raw})
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Errorf("algorithm %#v: error = %v, want ErrUnsupportedAlgorithm", algorithm, err)
		}
	}
}

func TestVerifyRejectsDigestUsedAsDecoyAndDisclosure(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-1", "given_name", "Alice")

	t.Run("same layer", func(t *testing.T) {
		payload := map[string]any{
			"_sd_alg": "sha-256",
			// The second occurrence is a decoy that collides with the real
			// disclosure slot.
			"_sd": []any{digest, digest},
		}
		_, err := Verify(payload, []string{raw})
		if !errors.Is(err, ErrDuplicateDigest) {
			t.Fatalf("error = %v, want ErrDuplicateDigest", err)
		}
	})

	t.Run("across layers", func(t *testing.T) {
		payload := map[string]any{
			"_sd_alg": "sha-256",
			"_sd":     []any{digest},
			"nested": map[string]any{
				// This occurrence is a decoy, but it has the same digest as
				// the real disclosure at the root layer.
				"_sd": []any{digest},
			},
		}
		_, err := Verify(payload, []string{raw})
		if !errors.Is(err, ErrDuplicateDigest) {
			t.Fatalf("error = %v, want ErrDuplicateDigest", err)
		}
	})

	t.Run("deep real disclosure collides with root decoy", func(t *testing.T) {
		raw, digest := mustObjectDisclosure(t, "salt-real", "real", "value")
		payload := map[string]any{
			"_sd_alg": "sha-256",
			// This root occurrence is a decoy.
			"_sd": []any{digest},
			"level1": map[string]any{
				"_sd": []any{"decoy-level1"},
				"level2": map[string]any{
					// This deeper occurrence is where the real disclosure
					// would be applied.
					"_sd": []any{digest},
				},
			},
		}
		if _, err := Verify(payload, []string{raw}); !errors.Is(err, ErrDuplicateDigest) {
			t.Fatalf("error = %v, want ErrDuplicateDigest", err)
		}
	})
}

func TestVerifyNestedSDInDisclosedValue(t *testing.T) {
	nameRaw, nameDigest := mustObjectDisclosure(t, "salt-name", "city", "Paris")
	profileRaw, profileDigest := mustObjectDisclosure(t, "salt-profile", "profile", map[string]any{
		"_sd": []any{nameDigest},
	})
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"_sd":     []any{profileDigest},
	}

	result, err := VerifyDetailed(payload, []string{profileRaw, nameRaw})
	if err != nil {
		t.Fatalf("VerifyDetailed: %v", err)
	}
	profile, ok := result.Claims["profile"].(map[string]any)
	if !ok {
		t.Fatalf("profile = %#v, want object", result.Claims["profile"])
	}
	if got := profile["city"]; got != "Paris" {
		t.Fatalf("profile.city = %v, want Paris", got)
	}
	if got := profile["_sd"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("profile._sd = %#v, want empty array", got)
	}
	if len(result.Disclosed) != 2 {
		t.Fatalf("disclosed count = %d, want 2", len(result.Disclosed))
	}
}

func TestVerifyTwoNestedSignedLayers(t *testing.T) {
	outerRaw, outerDigest := mustObjectDisclosure(t, "salt-outer", "outer", "value")
	innerRaw, innerDigest := mustObjectDisclosure(t, "salt-inner", "inner", "value")
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"_sd":     []any{outerDigest},
		"layer1": map[string]any{
			"_sd": []any{innerDigest},
		},
	}

	claims, err := Verify(payload, []string{outerRaw, innerRaw})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := claims["outer"]; got != "value" {
		t.Fatalf("outer = %v, want value", got)
	}
	layer1 := claims["layer1"].(map[string]any)
	if got := layer1["inner"]; got != "value" {
		t.Fatalf("layer1.inner = %v, want value", got)
	}
	if got := layer1["_sd"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("layer1._sd = %#v, want empty array", got)
	}
}

func TestVerifyKeepsUndisclosedEntriesAtEveryLayer(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-real", "real", "yes")
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"_sd":     []any{digest, "decoy-root"},
		"nested": map[string]any{
			"_sd": []any{"decoy-nested"},
		},
	}

	result, err := VerifyDetailed(payload, []string{raw})
	if err != nil {
		t.Fatalf("VerifyDetailed: %v", err)
	}
	if !reflect.DeepEqual(result.Claims["_sd"], []any{"decoy-root"}) {
		t.Fatalf("root _sd = %#v", result.Claims["_sd"])
	}
	nested := result.Claims["nested"].(map[string]any)
	if !reflect.DeepEqual(nested["_sd"], []any{"decoy-nested"}) {
		t.Fatalf("nested _sd = %#v", nested["_sd"])
	}
	if len(result.Undisclosed) != 2 {
		t.Fatalf("undisclosed count = %d, want 2: %#v", len(result.Undisclosed), result.Undisclosed)
	}
}

func TestVerifyRejectsUnmatchedAndConflictingDisclosures(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-1", "given_name", "Alice")

	t.Run("unmatched", func(t *testing.T) {
		payload := map[string]any{"_sd_alg": "sha-256", "_sd": []any{digest}}
		other, _ := mustObjectDisclosure(t, "salt-2", "other", "value")
		_, err := Verify(payload, []string{raw, other})
		if !errors.Is(err, ErrUnmatchedDisclosure) {
			t.Fatalf("error = %v, want ErrUnmatchedDisclosure", err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		payload := map[string]any{
			"_sd_alg":    "sha-256",
			"_sd":        []any{digest},
			"given_name": "Mallory",
		}
		_, err := Verify(payload, []string{raw})
		if !errors.Is(err, ErrClaimConflict) {
			t.Fatalf("error = %v, want ErrClaimConflict", err)
		}
	})
}

func TestVerifyArrayElementDisclosure(t *testing.T) {
	raw, digest := mustArrayDisclosure(t, "salt-array", "second")
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"items":   []any{"first", digest},
	}

	claims, err := Verify(payload, []string{raw})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := claims["items"]; !reflect.DeepEqual(got, []any{"first", "second"}) {
		t.Fatalf("items = %#v", got)
	}
}

func TestVerifyArrayPlaceholderDisclosure(t *testing.T) {
	raw, digest := mustArrayDisclosure(t, "salt-array", "second")
	payload := map[string]any{
		"_sd_alg": "sha-256",
		"items":   []any{map[string]any{"...": digest}},
	}

	claims, err := Verify(payload, []string{raw})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := claims["items"]; !reflect.DeepEqual(got, []any{"second"}) {
		t.Fatalf("items = %#v", got)
	}
}

func TestVerifyJSONAndCompact(t *testing.T) {
	raw, digest := mustObjectDisclosure(t, "salt-1", "given_name", "Alice")
	payload := map[string]any{"_sd_alg": "sha-256", "_sd": []any{digest}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	claims, err := VerifyJSON(data, []string{raw})
	if err != nil {
		t.Fatalf("VerifyJSON: %v", err)
	}
	if got := claims["given_name"]; got != "Alice" {
		t.Fatalf("given_name = %v", got)
	}

	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(data) + ".signature"
	result, err := VerifyCompact(jwt + "~" + raw + "~")
	if err != nil {
		t.Fatalf("VerifyCompact: %v", err)
	}
	if got := result.Claims["given_name"]; got != "Alice" {
		t.Fatalf("compact given_name = %v", got)
	}
}

func TestParseDisclosureRejectsMalformedInput(t *testing.T) {
	encode := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}

	tests := []string{
		"",
		"not base64!",
		encode("not an array"),
		encode([]any{"salt"}),
		encode([]any{nil, "name", "value"}),
		encode([]any{"salt", nil, "value"}),
		encode([]any{"salt", 42, "value"}),
	}
	for _, input := range tests {
		if _, err := ParseDisclosure(input); !errors.Is(err, ErrMalformedDisclosure) &&
			!errors.Is(err, ErrReservedClaim) {
			t.Errorf("ParseDisclosure(%q) error = %v", input, err)
		}
	}
}
