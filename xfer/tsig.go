package xfer

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"
)

// Key is a pre-shared TSIG key. A key is bound to exactly one zone and one
// generation; rotating a key means issuing a new generation and retiring the
// old one, after which signatures made with it are rejected.
type Key struct {
	ID         string
	Secret     []byte
	Zone       string
	Generation uint32
}

// Keyring verifies TSIG signatures and detects replays.
type Keyring struct {
	mu     sync.Mutex
	keys   map[string]Key
	seen   map[[sha256.Size]byte]int64 // MAC digest -> expiry unix time
	now    func() time.Time
	maxAge int64 // seconds a MAC is remembered for replay detection
}

// NewKeyring builds a keyring from the given keys. Later duplicates of a key
// ID replace earlier ones, which is how a key generation is retired.
func NewKeyring(keys ...Key) *Keyring {
	kr := &Keyring{
		keys:   make(map[string]Key, len(keys)),
		seen:   make(map[[sha256.Size]byte]int64),
		now:    time.Now,
		maxAge: 600,
	}
	for _, k := range keys {
		kr.keys[k.ID] = k
	}
	return kr
}

// signingPayload is the canonical byte string the HMAC covers. Every field
// that identifies the request is included so a signature cannot be transplanted
// onto a different zone, type, transport or serial.
func signingPayload(req *Request, tsig *TSIG) []byte {
	var buf bytes.Buffer
	var tmp [8]byte
	writeStr := func(s string) {
		binary.BigEndian.PutUint16(tmp[:2], uint16(len(s)))
		buf.Write(tmp[:2])
		buf.WriteString(s)
	}
	writeStr(req.Zone)
	buf.WriteByte(byte(req.Type))
	buf.WriteByte(byte(req.Proto))
	binary.BigEndian.PutUint32(tmp[:4], req.ClientSerial)
	buf.Write(tmp[:4])
	writeStr(tsig.KeyID)
	binary.BigEndian.PutUint32(tmp[:4], tsig.Generation)
	buf.Write(tmp[:4])
	binary.BigEndian.PutUint64(tmp[:8], uint64(tsig.TimeSigned))
	buf.Write(tmp[:8])
	return buf.Bytes()
}

// Sign produces the TSIG for a request with the given key. It is provided for
// clients and tests; servers normally only verify.
func Sign(req *Request, key Key, now time.Time, fudge int64) *TSIG {
	t := &TSIG{
		KeyID:      key.ID,
		Generation: key.Generation,
		TimeSigned: now.Unix(),
		Fudge:      fudge,
	}
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write(signingPayload(req, t))
	t.MAC = mac.Sum(nil)
	return t
}

// verify checks the request signature. It returns nil on success or a
// *DenyError describing why the signature was rejected. A valid signature
// is remembered so the exact same signed request cannot be replayed.
func (kr *Keyring) verify(req *Request) error {
	t := req.TSIG
	if t == nil {
		return denyf("TSIG required but absent")
	}
	kr.mu.Lock()
	key, ok := kr.keys[t.KeyID]
	kr.mu.Unlock()
	if !ok {
		return denyf("unknown TSIG key %q", t.KeyID)
	}
	if key.Zone != req.Zone {
		return denyf("key %q is not valid for zone %q", t.KeyID, req.Zone)
	}
	if key.Generation != t.Generation {
		return denyf("key %q generation %d retired (current %d)", t.KeyID, t.Generation, key.Generation)
	}
	now := kr.now().Unix()
	if skew := now - t.TimeSigned; skew > t.Fudge || skew < -t.Fudge {
		return denyf("TSIG time outside fudge window")
	}
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write(signingPayload(req, t))
	expect := mac.Sum(nil)
	if !hmac.Equal(expect, t.MAC) {
		return denyf("bad TSIG signature")
	}
	digest := sha256.Sum256(expect)
	kr.mu.Lock()
	defer kr.mu.Unlock()
	kr.pruneLocked(now)
	if _, replayed := kr.seen[digest]; replayed {
		return denyf("replayed TSIG signature")
	}
	kr.seen[digest] = now + kr.maxAge
	return nil
}

// valid reports whether the request carries an acceptable signature without
// consuming its replay slot. Admission uses it while evaluating rules; the
// final verify happens once, when the transfer is granted.
func (kr *Keyring) valid(req *Request) bool {
	t := req.TSIG
	if t == nil {
		return false
	}
	kr.mu.Lock()
	key, ok := kr.keys[t.KeyID]
	kr.mu.Unlock()
	if !ok || key.Zone != req.Zone || key.Generation != t.Generation {
		return false
	}
	now := kr.now().Unix()
	if skew := now - t.TimeSigned; skew > t.Fudge || skew < -t.Fudge {
		return false
	}
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write(signingPayload(req, t))
	return hmac.Equal(mac.Sum(nil), t.MAC)
}

func (kr *Keyring) pruneLocked(now int64) {
	for d, exp := range kr.seen {
		if exp < now {
			delete(kr.seen, d)
		}
	}
}
