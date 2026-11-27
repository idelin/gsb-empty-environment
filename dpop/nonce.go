package dpop

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"time"
)

const nonceVersion = "v1"

// NonceManager issues and verifies HMAC-signed DPoP-Nonce values. Nonces are
// bound to a rotation period; verification accepts the current period and the
// immediately preceding period so that rotation is seamless for in-flight
// requests.
type NonceManager struct {
	secret []byte
	period time.Duration
	now    func() time.Time
}

type nonceEnvelope struct {
	V   string `json:"v"`
	P   int64  `json:"p"`
	IAT int64  `json:"iat"`
	MAC string `json:"mac"`
}

// NewNonceManager creates a manager with the given server secret and rotation
// period. A zero period defaults to one hour. The secret must be at least
// 32 bytes of high-entropy random material.
func NewNonceManager(secret []byte, period time.Duration) (*NonceManager, error) {
	if len(secret) < 32 {
		return nil, newError(ErrUseDPoPNonce, "nonce manager secret must be at least 32 bytes")
	}
	if period <= 0 {
		period = time.Hour
	}
	return &NonceManager{
		secret: append([]byte(nil), secret...),
		period: period,
		now:    time.Now,
	}, nil
}

func (m *NonceManager) withClock(now func() time.Time) *NonceManager {
	m.now = now
	return m
}

func (m *NonceManager) periodAt(t time.Time) int64 {
	return t.Unix() / int64(m.period/time.Second)
}

// periodKey derives the per-period HMAC key from the master secret so that
// rotation requires no stored key state.
func (m *NonceManager) periodKey(period int64) []byte {
	var periodBytes [8]byte
	binary.BigEndian.PutUint64(periodBytes[:], uint64(period))
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte("dpop-nonce-period"))
	mac.Write(periodBytes[:])
	return mac.Sum(nil)
}

// Issue returns a fresh nonce for the current rotation period.
func (m *NonceManager) Issue() string {
	return m.issueAt(m.now())
}

func (m *NonceManager) issueAt(now time.Time) string {
	period := m.periodAt(now)
	iat := now.Unix()
	envelope := nonceEnvelope{
		V:   nonceVersion,
		P:   period,
		IAT: iat,
	}
	envelope.MAC = m.sign(period, envelope)

	raw, _ := json.Marshal(envelope)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (m *NonceManager) sign(period int64, envelope nonceEnvelope) string {
	mac := hmac.New(sha256.New, m.periodKey(period))
	mac.Write([]byte(nonceVersion))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strconv.FormatInt(envelope.P, 10)))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strconv.FormatInt(envelope.IAT, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify reports whether nonce is a well-formed, correctly signed value for
// the current period or the immediately preceding period.
func (m *NonceManager) Verify(nonce string) bool {
	return m.verifyAt(nonce, m.now())
}

func (m *NonceManager) verifyAt(nonce string, now time.Time) bool {
	if nonce == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		return false
	}
	var envelope nonceEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	if envelope.V != nonceVersion || envelope.IAT <= 0 || envelope.MAC == "" {
		return false
	}

	currentPeriod := m.periodAt(now)
	// Only the current and immediately preceding periods are accepted, which
	// bounds the replay lifetime while keeping rotation seamless.
	if envelope.P != currentPeriod && envelope.P != currentPeriod-1 {
		return false
	}
	expected := m.sign(envelope.P, envelope)
	return hmac.Equal([]byte(expected), []byte(envelope.MAC))
}
