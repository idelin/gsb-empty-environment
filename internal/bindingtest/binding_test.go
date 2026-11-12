package bindingtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"testing"

	netsec "example.org/netsec/q0095"
)

type realmTokenStruct struct {
	Nonce                  []byte   `cbor:"10,keyasint"`
	HashAlgID              string   `cbor:"44236,keyasint"`
	PublicKey              []byte   `cbor:"44237,keyasint"`
	InitialMeasurement     []byte   `cbor:"44238,keyasint"`
	ExtensibleMeasurements [][]byte `cbor:"44239,keyasint"`
	PublicKeyHashAlgID     string   `cbor:"44240,keyasint"`
}

type platformTokenStruct struct {
	Nonce            []byte `cbor:"10,keyasint"`
	HashAlgID        string `cbor:"2402,keyasint"`
	ImplementationID []byte
	InstanceID       []byte
}

type evidenceStruct struct {
	Platform []byte
	Realm    []byte
}

const (
	claimNonce                       = uint64(10)
	claimPlatformHashAlgorithm       = uint64(2402)
	claimRealmHashAlgorithm          = uint64(44236)
	claimRealmPublicKey              = uint64(44237)
	claimRealmInitialMeasurement     = uint64(44238)
	claimRealmExtensibleMeasurements = uint64(44239)
	claimRealmPublicKeyHashAlgorithm = uint64(44240)
	claimPlatformToken               = uint64(44234)
	claimRealmToken                  = uint64(44241)
)

type cborTag struct {
	number uint64
	value  any
}

func TestVerifyBindingValidCOSE(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x42}, 64)
	realmKey := bytes.Repeat([]byte{0x11}, 97)
	realmToken := makeCOSE(realmClaims(nonce, realmKey, "sha-256", nil))
	platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))

	if err := netsec.VerifyBinding(realmToken, platformToken, nonce); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if err := netsec.CheckBinding(nonce, platformToken, realmToken); err != nil {
		t.Fatalf("valid binding rejected with reordered arguments: %v", err)
	}
}

func TestVerifyBindingValidCCAcollection(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x24}, 64)
	realmKey := bytes.Repeat([]byte{0x33}, 97)
	realmToken := makeCOSE(realmClaims(nonce, realmKey, "sha-384", nil))
	platformToken := makeCOSE(platformClaims(hashSHA384(realmKey), "sha-384"))

	collection := cborEncode(cborTag{number: 399, value: map[any]any{
		claimPlatformToken: platformToken,
		claimRealmToken:    realmToken,
	}})
	if err := netsec.VerifyBinding(collection, nonce); err != nil {
		t.Fatalf("valid CCA collection rejected: %v", err)
	}

	// Some producers emit the two tokens as an ordered pair rather than the
	// CCA CBOR map collection.
	if err := netsec.VerifyBinding([]any{platformToken, realmToken}, nonce); err != nil {
		t.Fatalf("valid two-element collection rejected: %v", err)
	}

	jsonCollection := []byte(fmt.Sprintf(`{"44234":%q,"44241":%q}`,
		base64.StdEncoding.EncodeToString(platformToken),
		base64.StdEncoding.EncodeToString(realmToken)))
	if err := netsec.VerifyBinding(jsonCollection, nonce); err != nil {
		t.Fatalf("valid JSON collection rejected: %v", err)
	}
}

func TestVerifyBindingDistinctMeasurementAndKeyHashAlgorithms(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x2a}, 64)
	realmKey := bytes.Repeat([]byte{0x3a}, 97)
	claims := realmClaimsWithMeasurements(
		nonce,
		realmKey,
		"sha-512",
		bytes.Repeat([]byte{0x01}, 64),
		[][]byte{bytes.Repeat([]byte{0x02}, 64)},
	)
	claims[claimRealmPublicKeyHashAlgorithm] = "sha-256"
	realmToken := makeCOSE(claims)
	platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-512"))

	if err := netsec.VerifyBinding(realmToken, platformToken, nonce); err != nil {
		t.Fatalf("distinct measurement/key-hash algorithms rejected: %v", err)
	}
}

func TestVerifyBindingRejectsSplicedPlatformToken(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x51}, 64)
	realmKeyA := bytes.Repeat([]byte{0xa1}, 97)
	realmKeyB := bytes.Repeat([]byte{0xb2}, 97)

	realmTokenA := makeCOSE(realmClaims(nonce, realmKeyA, "sha-256", nil))
	platformTokenB := makeCOSE(platformClaims(hashSHA256(realmKeyB), "sha-256"))

	err := netsec.VerifyBinding(realmTokenA, platformTokenB, nonce)
	if !errors.Is(err, netsec.ErrChallengeMismatch) {
		t.Fatalf("spliced tokens: got %v, want ErrChallengeMismatch", err)
	}
}

func TestVerifyBindingRejectsCallerNonceMismatch(t *testing.T) {
	realmNonce := bytes.Repeat([]byte{0x61}, 64)
	callerNonce := bytes.Repeat([]byte{0x62}, 64)
	realmKey := bytes.Repeat([]byte{0x71}, 97)

	realmToken := makeCOSE(realmClaims(realmNonce, realmKey, "sha-256", nil))
	platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))

	err := netsec.VerifyBinding(realmToken, platformToken, callerNonce)
	if !errors.Is(err, netsec.ErrChallengeMismatch) {
		t.Fatalf("caller nonce mismatch: got %v, want ErrChallengeMismatch", err)
	}
}

func TestVerifyBindingRejectsMeasurementLengthMismatch(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x81}, 64)
	realmKey := bytes.Repeat([]byte{0x91}, 97)

	tests := []struct {
		name    string
		initial []byte
		ext     [][]byte
	}{
		{name: "initial measurement", initial: bytes.Repeat([]byte{1}, 31)},
		{name: "extensible measurement", initial: bytes.Repeat([]byte{1}, 32),
			ext: [][]byte{bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 33)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			realmToken := makeCOSE(realmClaimsWithMeasurements(nonce, realmKey, "sha-256", tc.initial, tc.ext))
			platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))
			err := netsec.VerifyBinding(realmToken, platformToken, nonce)
			if !errors.Is(err, netsec.ErrMeasurementLength) {
				t.Fatalf("got %v, want ErrMeasurementLength", err)
			}
		})
	}
}

func TestVerifyBindingRejectsDisallowedAlgorithm(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xa1}, 64)
	realmKey := bytes.Repeat([]byte{0xb1}, 97)
	realmToken := makeCOSE(realmClaims(nonce, realmKey, "sha-1", nil))
	platformToken := makeCOSE(platformClaims(sha1LikeHash(realmKey), "sha-1"))

	err := netsec.VerifyBinding(realmToken, platformToken, nonce)
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("got %v, want ErrAlgorithmNotAllowed", err)
	}

	// SHA-256 is cryptographically fine in general, but this policy only
	// permits SHA-384.
	realmToken = makeCOSE(realmClaims(nonce, realmKey, "sha-256", nil))
	platformToken = makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))
	err = netsec.VerifyBinding(realmToken, platformToken, nonce,
		netsec.Policy{AllowedHashAlgorithms: []netsec.HashAlgorithm{netsec.SHA384}})
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("policy allow-list failure: got %v, want ErrAlgorithmNotAllowed", err)
	}
}

func TestVerifyBindingAcceptsBase64Claims(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xc1}, 64)
	realmKey := bytes.Repeat([]byte{0xd1}, 97)
	realmClaimsMap := realmClaims(nonce, realmKey, "sha-256", nil)
	platformClaimsMap := platformClaims(hashSHA256(realmKey), "sha-256")

	realmJSONish := map[string]any{}
	for key, value := range realmClaimsMap {
		realmJSONish[fmt.Sprint(key)] = jsonify(value)
	}
	platformJSONish := map[string]any{}
	for key, value := range platformClaimsMap {
		platformJSONish[fmt.Sprint(key)] = jsonify(value)
	}

	if err := netsec.VerifyBinding(realmJSONish, platformJSONish, nonce); err != nil {
		t.Fatalf("base64-encoded claim maps rejected: %v", err)
	}
}

func TestVerifyBindingAcceptsClaimStructsAndContext(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xd1}, 64)
	realmKey := bytes.Repeat([]byte{0xe1}, 97)
	realm := realmTokenStruct{
		Nonce:                  nonce,
		HashAlgID:              "sha-256",
		PublicKey:              realmKey,
		InitialMeasurement:     bytes.Repeat([]byte{1}, 32),
		ExtensibleMeasurements: [][]byte{bytes.Repeat([]byte{2}, 32)},
		PublicKeyHashAlgID:     "sha-256",
	}
	platform := platformTokenStruct{
		Nonce:            hashSHA256(realmKey),
		HashAlgID:        "sha-256",
		ImplementationID: bytes.Repeat([]byte{0xaa}, 32),
		InstanceID:       bytes.Repeat([]byte{0xbb}, 32),
	}

	if err := netsec.Check(context.Background(), realm, platform, nonce); err != nil {
		t.Fatalf("claim structs rejected: %v", err)
	}
}

func TestVerifyBindingAcceptsEvidenceStructCollection(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x1f}, 64)
	realmKey := bytes.Repeat([]byte{0x2f}, 97)
	realmToken := makeCOSE(realmClaims(nonce, realmKey, "sha-256", nil))
	platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))

	evidence := evidenceStruct{Platform: platformToken, Realm: realmToken}
	if err := netsec.VerifyBinding(evidence, nonce); err != nil {
		t.Fatalf("evidence struct collection rejected: %v", err)
	}
}

func TestVerifyBindingRejectsMissingClaims(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xe1}, 64)
	realmKey := bytes.Repeat([]byte{0xf1}, 97)
	claims := realmClaims(nonce, realmKey, "sha-256", nil)
	delete(claims, claimRealmPublicKey)
	realmToken := makeCOSE(claims)
	platformToken := makeCOSE(platformClaims(hashSHA256(realmKey), "sha-256"))

	err := netsec.VerifyBinding(realmToken, platformToken, nonce)
	if !errors.Is(err, netsec.ErrMissingClaim) {
		t.Fatalf("got %v, want ErrMissingClaim", err)
	}
}

func realmClaims(nonce, publicKey []byte, alg string, _ [][]byte) map[any]any {
	initial := bytes.Repeat([]byte{0x07}, digestSize(alg))
	ext := [][]byte{
		bytes.Repeat([]byte{0x08}, digestSize(alg)),
		bytes.Repeat([]byte{0x09}, digestSize(alg)),
	}
	return realmClaimsWithMeasurements(nonce, publicKey, alg, initial, ext)
}

func realmClaimsWithMeasurements(nonce, publicKey []byte, alg string, initial []byte, ext [][]byte) map[any]any {
	extAny := make([]any, len(ext))
	for i := range ext {
		extAny[i] = ext[i]
	}
	return map[any]any{
		claimNonce:                       nonce,
		claimRealmHashAlgorithm:          alg,
		claimRealmPublicKey:              publicKey,
		claimRealmInitialMeasurement:     initial,
		claimRealmExtensibleMeasurements: extAny,
		claimRealmPublicKeyHashAlgorithm: alg,
	}
}

func platformClaims(challenge []byte, alg string) map[any]any {
	return map[any]any{
		claimNonce:                       challenge,
		claimPlatformHashAlgorithm:       alg,
		"cca-platform-implementation-id": bytes.Repeat([]byte{0xaa}, 32),
		"cca-platform-instance-id":       bytes.Repeat([]byte{0xbb}, 32),
	}
}

func makeCOSE(claims map[any]any) []byte {
	payload := cborEncode(claims)
	return cborEncode(cborTag{number: 18, value: []any{
		[]byte{0xa0},
		map[any]any{},
		payload,
		[]byte("signature"),
	}})
}

func digestSize(alg string) int {
	switch alg {
	case "sha-256":
		return 32
	case "sha-384":
		return 48
	case "sha-512":
		return 64
	default:
		return 32
	}
}

func hashSHA256(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func hashSHA384(value []byte) []byte {
	sum := sha512.Sum384(value)
	return sum[:]
}

func sha1LikeHash(value []byte) []byte {
	// The verifier rejects the algorithm before comparing lengths/hashes;
	// this stand-in only keeps the test token well-formed.
	sum := sha256.Sum256(value)
	return sum[:20]
}

func jsonify(value any) any {
	switch x := value.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = jsonify(x[i])
		}
		return out
	case string:
		return x
	default:
		return x
	}
}

func cborEncode(value any) []byte {
	switch x := value.(type) {
	case cborTag:
		return append(encodeHead(6, x.number), cborEncode(x.value)...)
	case nil:
		return []byte{0xf6}
	case bool:
		if x {
			return []byte{0xf5}
		}
		return []byte{0xf4}
	case int:
		return cborEncode(int64(x))
	case int64:
		if x >= 0 {
			return encodeHead(0, uint64(x))
		}
		return encodeHead(1, uint64(-1-x))
	case uint64:
		return encodeHead(0, x)
	case string:
		out := encodeHead(3, uint64(len(x)))
		return append(out, []byte(x)...)
	case []byte:
		out := encodeHead(2, uint64(len(x)))
		return append(out, x...)
	case []any:
		out := encodeHead(4, uint64(len(x)))
		for _, item := range x {
			out = append(out, cborEncode(item)...)
		}
		return out
	case [][]byte:
		out := encodeHead(4, uint64(len(x)))
		for _, item := range x {
			out = append(out, cborEncode(item)...)
		}
		return out
	case map[any]any:
		keys := make([]any, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		out := encodeHead(5, uint64(len(keys)))
		for _, key := range keys {
			out = append(out, cborEncode(key)...)
			out = append(out, cborEncode(x[key])...)
		}
		return out
	default:
		panic(fmt.Sprintf("cborEncode: unsupported %T", value))
	}
}

func encodeHead(major byte, value uint64) []byte {
	switch {
	case value < 24:
		return []byte{major<<5 | byte(value)}
	case value < 1<<8:
		return append([]byte{major<<5 | 24}, byte(value))
	case value < 1<<16:
		out := []byte{major<<5 | 25, 0, 0}
		binary.BigEndian.PutUint16(out[1:], uint16(value))
		return out
	case value < 1<<32:
		out := []byte{major<<5 | 26, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(out[1:], uint32(value))
		return out
	default:
		out := []byte{major<<5 | 27, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(out[1:], value)
		return out
	}
}
