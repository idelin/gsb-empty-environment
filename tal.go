package rpki

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

var ErrTAL = errors.New("rpki: invalid TAL")

// TAL is an offline RFC 8630-style Trust Anchor Locator. No URI is fetched.
type TAL struct {
	Name                 string
	URIs                 []string
	SubjectPublicKeyInfo []byte
}

type jsonTAL struct {
	Name                 string   `json:"name"`
	URIs                 []string `json:"uris"`
	SubjectPublicKeyInfo string   `json:"subjectPublicKeyInfo"`
	SPKIBase64           string   `json:"spkiBase64"`
}

// ParseTAL parses the conventional text TAL format or a small JSON encoding.
func ParseTAL(name string, data []byte) (TAL, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		return parseJSONTAL(name, data)
	}
	tal := TAL{Name: name}
	var b64 strings.Builder
	inKey := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			inKey = len(tal.URIs) > 0
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !inKey && strings.Contains(line, "://") {
			tal.URIs = append(tal.URIs, line)
			continue
		}
		inKey = true
		b64.WriteString(line)
	}
	der, err := decodeTALKey(b64.String())
	if err != nil {
		return TAL{}, err
	}
	tal.SubjectPublicKeyInfo = der
	if _, err := parseTALPublicKey(der); err != nil {
		return TAL{}, err
	}
	if len(tal.URIs) == 0 {
		return TAL{}, fmt.Errorf("%w: %s has no locator URI", ErrTAL, name)
	}
	return tal, nil
}

func parseJSONTAL(name string, data []byte) (TAL, error) {
	var wire jsonTAL
	if err := json.Unmarshal(data, &wire); err != nil {
		return TAL{}, fmt.Errorf("%w: JSON: %v", ErrTAL, err)
	}
	encoded := wire.SubjectPublicKeyInfo
	if encoded == "" {
		encoded = wire.SPKIBase64
	}
	der, err := decodeTALKey(encoded)
	if err != nil {
		return TAL{}, err
	}
	if _, err := parseTALPublicKey(der); err != nil {
		return TAL{}, err
	}
	if wire.Name == "" {
		wire.Name = name
	}
	if len(wire.URIs) == 0 {
		return TAL{}, fmt.Errorf("%w: %s has no uris", ErrTAL, name)
	}
	return TAL{Name: wire.Name, URIs: wire.URIs, SubjectPublicKeyInfo: der}, nil
}

func decodeTALKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("%w: missing trust anchor public key", ErrTAL)
	}
	if block, _ := pem.Decode([]byte(encoded)); block != nil {
		return block.Bytes, nil
	}
	compact := strings.NewReplacer("-----BEGIN PUBLIC KEY-----", "", "-----END PUBLIC KEY-----", "", "\n", "", "\r", "", " ", "", "\t", "").Replace(encoded)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if der, err := encoding.DecodeString(compact); err == nil {
			return der, nil
		}
	}
	return nil, fmt.Errorf("%w: public key is not base64/PEM SubjectPublicKeyInfo", ErrTAL)
}

func parseTALPublicKey(der []byte) (any, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: ParsePKIXPublicKey: %v", ErrTAL, err)
	}
	return pub, nil
}

// ECDSAP256PublicKey returns the key when the TAL pins an ECDSA P-256 key.
func (t TAL) ECDSAP256PublicKey() (*ecdsa.PublicKey, error) {
	pub, err := parseTALPublicKey(t.SubjectPublicKeyInfo)
	if err != nil {
		return nil, err
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: expected ECDSA public key, got %T", ErrTAL, pub)
	}
	if ec.Curve == nil || ec.Curve.Params() == nil || ec.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("%w: expected ECDSA P-256, got %s", ErrTAL, curveName(ec))
	}
	return ec, nil
}

func curveName(key *ecdsa.PublicKey) string {
	if key == nil || key.Curve == nil || key.Curve.Params() == nil {
		return "unknown"
	}
	return key.Curve.Params().Name
}

// WriteText renders the canonical TAL text form.
func (t TAL) WriteText() []byte {
	var b strings.Builder
	for _, uri := range t.URIs {
		b.WriteString(uri)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(wrapBase64(base64.StdEncoding.EncodeToString(t.SubjectPublicKeyInfo), 64))
	b.WriteByte('\n')
	return []byte(b.String())
}

func wrapBase64(value string, width int) string {
	var out strings.Builder
	for i := 0; i < len(value); i += width {
		end := i + width
		if end > len(value) {
			end = len(value)
		}
		out.WriteString(value[i:end])
		out.WriteByte('\n')
	}
	return strings.TrimRight(out.String(), "\n")
}
