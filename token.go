package delegation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
)

// tokenVersion prefixes every serialized token so that future token formats
// can be distinguished.
const tokenVersion = "v1"

// normalizeSet returns a sorted, deduplicated copy of values. Blank entries
// make the set invalid.
func normalizeSet(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, ErrMalformedScope
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			return nil, ErrMalformedScope
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func contains(values []string, want string) bool {
	idx := sort.SearchStrings(values, want)
	return idx < len(values) && values[idx] == want
}

// isSubset reports whether every element of sub is present in super. Both
// slices are expected to be sorted.
func isSubset(sub, super []string) bool {
	if len(sub) == 0 {
		return false
	}
	for _, value := range sub {
		if !contains(super, value) {
			return false
		}
	}
	return true
}

// signToken serializes the ticket and appends an HMAC-SHA256 tag bound to
// the service key. The resulting token is safe to hand to job workers:
// tampering is detected on parse because the tag will no longer match.
func signToken(key []byte, ticket Ticket) (string, error) {
	payload, err := json.Marshal(ticket)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return tokenVersion + "." + encoded + "." + signature, nil
}

// parseToken verifies the token structure and HMAC tag and returns the
// decoded ticket. It performs no stateful validation (revocation, expiry or
// consumption are checked separately by the Service).
func parseToken(key []byte, token string) (Ticket, error) {
	var ticket Ticket
	version, encoded, signature, ok := split3(token, '.')
	if !ok || version != tokenVersion {
		return ticket, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encoded))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ticket, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ticket, ErrInvalidToken
	}
	if err := json.Unmarshal(payload, &ticket); err != nil || ticket.ID == "" {
		return Ticket{}, ErrInvalidToken
	}
	return ticket, nil
}

func split3(s string, sep byte) (a, b, c string, ok bool) {
	first := strings.IndexByte(s, sep)
	if first < 0 {
		return "", "", "", false
	}
	rest := s[first+1:]
	second := strings.IndexByte(rest, sep)
	if second < 0 {
		return "", "", "", false
	}
	return s[:first], rest[:second], rest[second+1:], true
}
