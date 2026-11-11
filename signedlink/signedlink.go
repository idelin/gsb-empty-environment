// Package signedlink implements HMAC-signed URLs for a media distribution
// service. The signer and the verification middleware share one bounded
// canonical encoding so that distinct requests can never collapse onto the
// same signature through concatenation ambiguity.
package signedlink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Signature query parameters. Exactly one of each must be present on a
// signed request; duplicates are rejected by the middleware.
const (
	ParamSig     = "sig"
	ParamExpires = "expires"
	ParamKeyID   = "kid"
)

// Key is a single HMAC secret. NotAfter bounds verification: a retired key
// keeps verifying only inside its grace window and is refused afterwards.
type Key struct {
	ID       string
	Secret   []byte
	NotAfter time.Time
}

// KeyStore holds the active signing key plus retired keys still inside
// their grace window.
type KeyStore struct {
	active string
	keys   map[string]Key
}

func NewKeyStore(keys []Key, active string) (*KeyStore, error) {
	ks := &KeyStore{keys: make(map[string]Key, len(keys)), active: active}
	for _, k := range keys {
		if k.ID == "" {
			return nil, errors.New("signedlink: key with empty ID")
		}
		if len(k.Secret) == 0 {
			return nil, fmt.Errorf("signedlink: key %q has empty secret", k.ID)
		}
		if _, dup := ks.keys[k.ID]; dup {
			return nil, fmt.Errorf("signedlink: duplicate key ID %q", k.ID)
		}
		ks.keys[k.ID] = k
	}
	if _, ok := ks.keys[active]; !ok {
		return nil, fmt.Errorf("signedlink: active key %q not found", active)
	}
	return ks, nil
}

// canonical encodes one request into an unambiguous, length-prefixed byte
// string. Every variable-length field carries its length, so no two
// distinct (method, path, expires, params) tuples can encode to the same
// bytes. Both the signer and the middleware use this exact encoding.
func canonical(method, escapedPath string, expires int64, params url.Values) string {
	var b strings.Builder
	b.WriteString("sig-v1\n")
	writeField(&b, method)
	writeField(&b, escapedPath)
	// Expiry is a fixed canonical decimal integer.
	b.WriteString(strconv.FormatInt(expires, 10))
	b.WriteByte('\n')

	keys := make([]string, 0, len(params))
	for k := range params {
		if k == ParamSig {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := append([]string(nil), params[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			writeField(&b, k)
			writeField(&b, v)
		}
	}
	return b.String()
}

// writeField writes one length-prefixed field: "<len>:<bytes>\n". Empty
// values encode as "0:" and stay distinct from absent keys.
func writeField(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
	b.WriteByte('\n')
}

func computeMAC(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignURL returns rawURL with kid, expires and sig parameters attached,
// signed with the store's active key. The signature binds the HTTP method,
// the escaped request path, the expiry and every query parameter
// (duplicates and empty values included).
func (ks *KeyStore) SignURL(method, rawURL string, expires time.Time, extra url.Values) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("signedlink: parse URL: %w", err)
	}
	key := ks.keys[ks.active]
	exp := expires.Unix()

	q := u.Query()
	for _, reserved := range []string{ParamSig, ParamExpires, ParamKeyID} {
		if _, exists := q[reserved]; exists {
			return "", fmt.Errorf("signedlink: URL already contains reserved parameter %q", reserved)
		}
	}
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	q.Set(ParamKeyID, key.ID)
	q.Set(ParamExpires, strconv.FormatInt(exp, 10))

	payload := canonical(method, u.EscapedPath(), exp, q)
	q.Set(ParamSig, computeMAC(key.Secret, payload))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Verify checks a request's signature against the store. now is injected so
// expiry and key-grace checks are deterministic in tests.
func (ks *KeyStore) Verify(method, escapedPath string, query url.Values, now time.Time) error {
	sigs, ok := query[ParamSig]
	if !ok || len(sigs) == 0 {
		return errors.New("signedlink: missing signature")
	}
	if len(sigs) != 1 {
		return errors.New("signedlink: duplicate signature field")
	}
	for _, reserved := range []string{ParamExpires, ParamKeyID} {
		if n := len(query[reserved]); n != 1 {
			return fmt.Errorf("signedlink: parameter %q must appear exactly once, got %d", reserved, n)
		}
	}

	exp, err := strconv.ParseInt(query.Get(ParamExpires), 10, 64)
	if err != nil {
		return fmt.Errorf("signedlink: malformed expires: %w", err)
	}
	if now.Unix() > exp {
		return errors.New("signedlink: link expired")
	}

	key, ok := ks.keys[query.Get(ParamKeyID)]
	if !ok {
		return errors.New("signedlink: unknown key ID")
	}
	if !key.NotAfter.IsZero() && now.After(key.NotAfter) {
		return errors.New("signedlink: key past grace window")
	}

	payload := canonical(method, escapedPath, exp, query)
	want := computeMAC(key.Secret, payload)
	if !hmac.Equal([]byte(want), []byte(sigs[0])) {
		return errors.New("signedlink: signature mismatch")
	}
	return nil
}
