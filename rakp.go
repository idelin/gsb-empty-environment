package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"time"
)

// RAKP2PayloadLen is the fixed wire length of every RAKP2 response:
// console session ID (4) + BMC random (16) + BMC GUID (16) + auth code (32).
// It does not depend on whether the requested username exists.
const RAKP2PayloadLen = 4 + 16 + 16 + sha256.Size

const rakp3AuthCodeLen = sha256.Size

var (
	// ErrDropped means the source exceeded the RAKP failure budget and
	// its session requests are discarded.
	ErrDropped = errors.New("netsec: session request dropped by failure throttle")
	// ErrAnonymous rejects anonymous logins (empty username and password).
	ErrAnonymous = errors.New("netsec: anonymous login is not permitted")
	// ErrAuthFailed is the single error for RAKP3 verification failure;
	// it deliberately does not distinguish unknown users from bad passwords.
	ErrAuthFailed = errors.New("netsec: RAKP authentication failed")
	// ErrUnknownSession means no pending RAKP exchange for the session ID.
	ErrUnknownSession = errors.New("netsec: unknown RAKP session")
)

// UserStore resolves usernames to their BMC password.
type UserStore interface {
	Password(username string) (password []byte, ok bool)
}

// MapUserStore is a simple in-memory UserStore.
type MapUserStore map[string][]byte

func (m MapUserStore) Password(username string) ([]byte, bool) {
	pw, ok := m[username]
	return pw, ok
}

// RAKP1Request carries the fields of an RMCP+ RAKP Message 1 relevant to
// admission and authentication.
type RAKP1Request struct {
	SourceAddr  string
	SuiteID     uint8
	SessionID   uint32
	ConsoleRand [16]byte
	Username    string
}

// RAKP3Request carries the client's proof of password knowledge.
type RAKP3Request struct {
	SourceAddr string
	SessionID  uint32
	AuthCode   []byte
}

type pendingSession struct {
	expected []byte
}

// Authenticator drives the RAKP exchange for RMCP+ session establishment.
// Unknown usernames produce RAKP2 responses identical in length and time
// bucket to wrong-password responses, so callers cannot enumerate users.
type Authenticator struct {
	registry *Registry
	users    UserStore
	clock    Clock
	throttle *Throttle
	bucket   time.Duration
	dummyKey []byte
	guid     [16]byte
	sessions map[uint32]*pendingSession
}

// NewAuthenticator builds an Authenticator. bucket is the response-time
// quantum: every RAKP reply is padded (via clock.Sleep) up to a whole
// number of buckets so unknown-user and wrong-password paths match.
func NewAuthenticator(reg *Registry, users UserStore, clock Clock, bucket time.Duration) *Authenticator {
	a := &Authenticator{
		registry: reg,
		users:    users,
		clock:    clock,
		throttle: NewThrottle(DefaultThrottleWindow, DefaultThrottleMaxFailures),
		bucket:   bucket,
		dummyKey: make([]byte, sha256.Size),
		sessions: make(map[uint32]*pendingSession),
	}
	if _, err := rand.Read(a.dummyKey); err != nil {
		panic(err)
	}
	if _, err := rand.Read(a.guid[:]); err != nil {
		panic(err)
	}
	return a
}

// HandleRAKP1 processes RAKP Message 1 and returns the fixed-length RAKP2
// payload. Unknown usernames still receive a well-formed RAKP2 computed
// with a random dummy key; the exchange only fails later at RAKP3 with
// the same error a wrong password yields.
func (a *Authenticator) HandleRAKP1(req *RAKP1Request) ([]byte, error) {
	start := a.clock.Now()
	if a.throttle.Dropped(req.SourceAddr, start) {
		return nil, ErrDropped
	}
	if _, err := a.registry.Admit(req.SuiteID); err != nil {
		a.throttle.RecordFailure(req.SourceAddr, a.clock.Now())
		return nil, err
	}
	if req.Username == "" {
		a.throttle.RecordFailure(req.SourceAddr, a.clock.Now())
		return nil, ErrAnonymous
	}

	key, known := a.users.Password(req.Username)
	if !known {
		// Same work, same response shape: use a per-instance random key
		// so RAKP2 is indistinguishable from a real user's response.
		key = a.dummyKey
	}

	var bmcRand [16]byte
	if _, err := rand.Read(bmcRand[:]); err != nil {
		return nil, err
	}

	a.sessions[req.SessionID] = &pendingSession{
		expected: RAKP3AuthCode(key, req.SessionID, req.ConsoleRand, bmcRand, a.guid),
	}
	payload := buildRAKP2(key, req, bmcRand, a.guid)
	a.padToBucket(start)
	return payload, nil
}

// HandleRAKP3 verifies the client's proof. Unknown users and wrong
// passwords both fail here with ErrAuthFailed.
func (a *Authenticator) HandleRAKP3(req *RAKP3Request) error {
	start := a.clock.Now()
	defer a.padToBucket(start)
	if a.throttle.Dropped(req.SourceAddr, start) {
		return ErrDropped
	}
	sess, ok := a.sessions[req.SessionID]
	if !ok {
		return ErrUnknownSession
	}
	delete(a.sessions, req.SessionID)
	if len(req.AuthCode) != rakp3AuthCodeLen ||
		subtle.ConstantTimeCompare(req.AuthCode, sess.expected) != 1 {
		a.throttle.RecordFailure(req.SourceAddr, a.clock.Now())
		return ErrAuthFailed
	}
	return nil
}

// padToBucket sleeps until the elapsed time since start reaches the next
// whole bucket, keeping every response inside the same time quantum.
func (a *Authenticator) padToBucket(start time.Time) {
	if a.bucket <= 0 {
		return
	}
	elapsed := a.clock.Now().Sub(start)
	a.clock.Sleep(a.bucket - elapsed%a.bucket)
}

// RAKP3AuthCode computes the expected RAKP Message 3 auth code. It is
// exported so client-side callers can prove password knowledge.
func RAKP3AuthCode(password []byte, sessionID uint32, consoleRand, bmcRand, guid [16]byte) []byte {
	mac := hmac.New(sha256.New, password)
	var sid [4]byte
	binary.BigEndian.PutUint32(sid[:], sessionID)
	mac.Write([]byte("rakp3"))
	mac.Write(sid[:])
	mac.Write(consoleRand[:])
	mac.Write(bmcRand[:])
	mac.Write(guid[:])
	return mac.Sum(nil)
}

func buildRAKP2(key []byte, req *RAKP1Request, bmcRand, guid [16]byte) []byte {
	buf := make([]byte, 0, RAKP2PayloadLen)
	var sid [4]byte
	binary.BigEndian.PutUint32(sid[:], req.SessionID)
	buf = append(buf, sid[:]...)
	buf = append(buf, bmcRand[:]...)
	buf = append(buf, guid[:]...)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("rakp2"))
	mac.Write(buf)
	mac.Write(req.ConsoleRand[:])
	mac.Write([]byte(req.Username))
	return mac.Sum(buf)
}
