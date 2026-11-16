package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// zookieVersion is the wire-format version understood by this package.
const zookieVersion = 1

// Sentinel errors returned by the snapshot-consistency machinery. Callers
// should use errors.Is rather than matching on message text.
var (
	// ErrMalformedZookie is returned when a token is syntactically invalid,
	// uses an unsupported version, carries a bad timestamp, or fails HMAC
	// authentication. Such requests are rejected rather than served.
	ErrMalformedZookie = errors.New("netsec: malformed zookie")

	// ErrFutureZookie is returned when a token claims a timestamp newer than
	// any zookie the leader has ever issued. The token cannot name a real
	// snapshot and must be rejected.
	ErrFutureZookie = errors.New("netsec: zookie is ahead of the leader's latest timestamp")

	// ErrStaleReplica is returned when a replica cannot serve a check because
	// its applied snapshot is older than the required freshness and waiting
	// did not close the gap (and leader forwarding is disabled).
	ErrStaleReplica = errors.New("netsec: replica snapshot is behind the required freshness")
)

// Zookie is a Zanzibar-style timestamp token. Every write produces one with a
// strictly increasing timestamp. The wire token is authenticated with an
// HMAC-SHA256 so that callers cannot forge or tamper with freshness claims.
type Zookie struct {
	token     string
	timestamp uint64
}

// Token returns the opaque, authenticated wire representation.
func (z Zookie) Token() string { return z.token }

// Timestamp returns the monotonically increasing logical timestamp. It is
// zero only for the invalid zero Zookie.
func (z Zookie) Timestamp() uint64 { return z.timestamp }

// IsZero reports whether z is the invalid zero value.
func (z Zookie) IsZero() bool { return z.timestamp == 0 }

// String returns the wire token.
func (z Zookie) String() string { return z.token }

// mintZookie issues an authenticated token for timestamp ts. It is unexported:
// only the leader store may create valid tokens.
func mintZookie(secret []byte, ts uint64) Zookie {
	payload := strconv.Itoa(zookieVersion) + ":" + strconv.FormatUint(ts, 10)
	token := payload + ":" + signZookie(secret, payload)
	return Zookie{token: token, timestamp: ts}
}

// ParseZookie parses and authenticates a wire token. Any structural problem,
// unsupported version, non-positive timestamp, or signature mismatch yields
// ErrMalformedZookie.
func ParseZookie(secret []byte, token string) (Zookie, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return Zookie{}, fmt.Errorf("%w: expected version:timestamp:signature", ErrMalformedZookie)
	}

	version, err := strconv.Atoi(parts[0])
	if err != nil || version != zookieVersion {
		return Zookie{}, fmt.Errorf("%w: unsupported version", ErrMalformedZookie)
	}

	ts, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return Zookie{}, fmt.Errorf("%w: bad timestamp", ErrMalformedZookie)
	}
	if ts == 0 {
		return Zookie{}, fmt.Errorf("%w: zero timestamp", ErrMalformedZookie)
	}

	payload := parts[0] + ":" + parts[1]
	if !hmac.Equal([]byte(signZookie(secret, payload)), []byte(parts[2])) {
		return Zookie{}, fmt.Errorf("%w: signature mismatch", ErrMalformedZookie)
	}

	return Zookie{token: token, timestamp: ts}, nil
}

func signZookie(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
