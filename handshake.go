package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrHandshakeState is returned for out-of-order or replayed state
	// transitions.
	ErrHandshakeState = errors.New("netsec: invalid handshake state")
	// ErrSignature is returned when an Ed25519 signature does not verify.
	ErrSignature = errors.New("netsec: signature verification failed")
	// ErrConfirmation is returned when a confirmation MAC does not verify.
	ErrConfirmation = errors.New("netsec: confirmation verification failed")
	// ErrIdentityMismatch is returned when a name label does not match the
	// identity the host resolved for that session.
	ErrIdentityMismatch = errors.New("netsec: identity mismatch")
	// ErrInvalidMode is returned for an unknown protocol mode.
	ErrInvalidMode = errors.New("netsec: invalid protocol mode")
)

// InitiatorConfig configures A's side of one session.
type InitiatorConfig struct {
	Mode      Mode
	SessionID []byte
	Peer      string
	Ephemeral *EphemeralKey
	Rand      io.Reader
}

// ResponderConfig configures B's side of one session. ExpectedPeer optionally
// pins the initiator label; the hardened protocol must remain safe even when it
// is empty and the host accepts any registered initiator name.
type ResponderConfig struct {
	Mode         Mode
	ExpectedPeer string
	Ephemeral    *EphemeralKey
	Rand         io.Reader
}

// Initiator is A's per-session state machine. It is not safe for concurrent use.
type Initiator struct {
	party     *Party
	mode      Mode
	peer      Identity
	ephemeral *EphemeralKey
	hello     *Hello

	x         []byte
	y         []byte
	digest    [32]byte
	keys      KeySchedule
	samePoint bool

	responseVerified bool
	auth             *InitiatorAuth
	confirmA         *Confirmation
	confirmASent     bool
	peerConfirmed    bool
}

// Responder is B's per-session state machine. It is not safe for concurrent use.
type Responder struct {
	party     *Party
	mode      Mode
	peer      Identity
	ephemeral *EphemeralKey
	hello     *Hello
	response  *Response

	x         []byte
	y         []byte
	digest    [32]byte
	keys      KeySchedule
	samePoint bool

	authVerified     bool
	confirmAVerified bool
	confirmBSent     bool
}

// NewInitiator starts A's side, reserves the session ID on A's host, and emits
// the Hello containing the session ID, A's name label, and X.
func NewInitiator(party *Party, cfg InitiatorConfig) (*Initiator, *Hello, error) {
	if party == nil {
		return nil, nil, errors.New("netsec: nil initiator party")
	}
	if !cfg.Mode.IsValid() {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidMode, cfg.Mode)
	}
	if err := ValidateSessionID(cfg.SessionID); err != nil {
		return nil, nil, err
	}
	if cfg.Peer == "" {
		return nil, nil, fmt.Errorf("%w: empty peer identity", ErrInvalidIdentityName)
	}
	peer, err := party.Registry.Lookup(cfg.Peer)
	if err != nil {
		return nil, nil, err
	}
	ephemeral := cfg.Ephemeral
	if ephemeral == nil {
		ephemeral, err = GenerateEphemeral(cfg.Rand)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(ephemeral.PublicBytes()) == 0 {
		return nil, nil, ErrInvalidPoint
	}
	if err := party.Sessions.Reserve(cfg.SessionID); err != nil {
		return nil, nil, err
	}
	hello := &Hello{
		SessionID: append([]byte(nil), cfg.SessionID...),
		Initiator: party.ID.Name,
		X:         ephemeral.PublicBytes(),
	}
	return &Initiator{
		party:     party,
		mode:      cfg.Mode,
		peer:      cloneIdentity(peer),
		ephemeral: ephemeral,
		hello:     cloneHello(hello),
		x:         ephemeral.PublicBytes(),
	}, hello, nil
}

// Hello returns a defensive copy of A's first message.
func (i *Initiator) Hello() *Hello {
	if i == nil {
		return nil
	}
	return cloneHello(i.hello)
}

// Peer returns the identity A currently believes it is talking to.
func (i *Initiator) Peer() (Identity, bool) {
	if i == nil || i.peer.Name == "" {
		return Identity{}, false
	}
	return cloneIdentity(i.peer), true
}

// HandleResponse verifies B's signature, applies the same-point rule, derives
// the key schedule, and returns A's signature over the same digest. A does not
// send a confirmation until this method succeeds.
func (i *Initiator) HandleResponse(response *Response) (*InitiatorAuth, error) {
	if i == nil || i.hello == nil {
		return nil, ErrHandshakeState
	}
	if i.responseVerified {
		return nil, fmt.Errorf("%w: response already processed", ErrHandshakeState)
	}
	if response == nil {
		return nil, fmt.Errorf("%w: nil response", ErrHandshakeState)
	}
	if !bytes.Equal(response.SessionID, i.hello.SessionID) {
		return nil, fmt.Errorf("%w: response session ID mismatch", ErrHandshakeState)
	}
	if response.Responder == "" {
		return nil, fmt.Errorf("%w: empty responder label", ErrIdentityMismatch)
	}
	responder, err := i.party.Registry.Lookup(response.Responder)
	if err != nil {
		return nil, err
	}
	if i.peer.Name != responder.Name {
		return nil, fmt.Errorf("%w: expected %q, response claims %q", ErrIdentityMismatch, i.peer.Name, responder.Name)
	}
	_, _, y, err := ParseP256Point(response.Y)
	if err != nil {
		return nil, fmt.Errorf("netsec: response Y: %w", err)
	}
	if bytes.Equal(y, i.x) {
		if err := SamePointRule(i.ephemeral.PublicBytes(), y); err != nil {
			return nil, err
		}
		i.samePoint = true
	}

	transcript := Transcript{
		Mode:      i.mode,
		SessionID: i.hello.SessionID,
		Initiator: i.party.ID,
		Responder: responder,
		X:         i.x,
		Y:         y,
	}
	digest, err := transcript.Digest()
	if err != nil {
		return nil, err
	}
	if len(response.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(responder.PublicKey, digest[:], response.Signature) {
		return nil, fmt.Errorf("%w: responder %q", ErrSignature, responder.Name)
	}
	shared, err := i.ephemeral.SharedPoint(y)
	if err != nil {
		return nil, err
	}
	keys, err := DeriveKeySchedule(shared, digest[:])
	if err != nil {
		return nil, err
	}
	signature := i.party.sign(digest[:])
	auth := &InitiatorAuth{
		SessionID: append([]byte(nil), i.hello.SessionID...),
		Initiator: i.party.ID.Name,
		Signature: signature,
	}

	i.y = y
	i.digest = digest
	i.keys = keys
	i.peer = cloneIdentity(responder)
	i.responseVerified = true
	i.auth = cloneAuth(auth)
	return auth, nil
}

// SendConfirmA returns A's role-bound confirmation. It requires a verified
// response. A must send this before accepting B's confirmation.
func (i *Initiator) SendConfirmA() (*Confirmation, error) {
	if i == nil || !i.responseVerified {
		return nil, fmt.Errorf("%w: response not verified", ErrHandshakeState)
	}
	if i.confirmASent {
		return nil, fmt.Errorf("%w: confirmation A already sent", ErrHandshakeState)
	}
	confirmation := &Confirmation{
		SessionID: append([]byte(nil), i.hello.SessionID...),
		Role:      RoleA,
		MAC:       confirmationMAC(i.keys.Confirm, RoleA, i.digest),
	}
	i.confirmA = cloneConfirmation(confirmation)
	i.confirmASent = true
	return confirmation, nil
}

// VerifyResponderConfirm checks B's role-bound confirmation. It also enforces
// ordering: A must have sent its own confirmation first.
func (i *Initiator) VerifyResponderConfirm(confirmation *Confirmation) error {
	if i == nil || !i.confirmASent {
		return fmt.Errorf("%w: initiator confirmation not sent first", ErrHandshakeState)
	}
	if i.peerConfirmed {
		return fmt.Errorf("%w: responder confirmation already processed", ErrHandshakeState)
	}
	if confirmation == nil {
		return fmt.Errorf("%w: nil confirmation", ErrHandshakeState)
	}
	if !bytes.Equal(confirmation.SessionID, i.hello.SessionID) {
		return fmt.Errorf("%w: confirmation session ID mismatch", ErrHandshakeState)
	}
	if confirmation.Role != RoleB {
		return fmt.Errorf("%w: expected role B, got %v", ErrConfirmation, confirmation.Role)
	}
	expected := confirmationMAC(i.keys.Confirm, RoleB, i.digest)
	if !hmacEqual(expected, confirmation.MAC) {
		return ErrConfirmation
	}
	i.peerConfirmed = true
	return nil
}

// Keys releases A's traffic keys only after B's confirmation has verified. This
// makes X == Y and reflected X cases depend on proof that the peer holds the
// matching ephemeral private key.
func (i *Initiator) Keys() (KeySchedule, error) {
	if i == nil || !i.peerConfirmed {
		return KeySchedule{}, fmt.Errorf("%w: responder confirmation not verified", ErrHandshakeState)
	}
	return i.keys, nil
}

// HandshakeHash returns A's transcript digest after the response is verified.
func (i *Initiator) HandshakeHash() ([32]byte, error) {
	if i == nil || !i.responseVerified {
		return [32]byte{}, fmt.Errorf("%w: response not verified", ErrHandshakeState)
	}
	return i.digest, nil
}

// SamePoint reports whether X == Y for this session.
func (i *Initiator) SamePoint() bool { return i != nil && i.samePoint }

// NewResponder consumes A's Hello, resolves the initiator name through the
// host registry, validates X, generates Y, signs the transcript digest, and
// derives B's keys.
func NewResponder(party *Party, cfg ResponderConfig, hello *Hello) (*Responder, *Response, error) {
	if party == nil {
		return nil, nil, errors.New("netsec: nil responder party")
	}
	if !cfg.Mode.IsValid() {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidMode, cfg.Mode)
	}
	if hello == nil {
		return nil, nil, fmt.Errorf("%w: nil hello", ErrHandshakeState)
	}
	if err := ValidateSessionID(hello.SessionID); err != nil {
		return nil, nil, err
	}
	if hello.Initiator == "" {
		return nil, nil, fmt.Errorf("%w: empty initiator label", ErrIdentityMismatch)
	}
	peer, err := party.Registry.Lookup(hello.Initiator)
	if err != nil {
		return nil, nil, err
	}
	if cfg.ExpectedPeer != "" && cfg.ExpectedPeer != peer.Name {
		return nil, nil, fmt.Errorf("%w: expected %q, hello claims %q", ErrIdentityMismatch, cfg.ExpectedPeer, peer.Name)
	}
	_, _, x, err := ParseP256Point(hello.X)
	if err != nil {
		return nil, nil, fmt.Errorf("netsec: hello X: %w", err)
	}
	ephemeral := cfg.Ephemeral
	if ephemeral == nil {
		ephemeral, err = GenerateEphemeral(cfg.Rand)
		if err != nil {
			return nil, nil, err
		}
	}
	y := ephemeral.PublicBytes()
	if bytes.Equal(x, y) {
		if err := SamePointRule(y, x); err != nil {
			return nil, nil, err
		}
	}
	transcript := Transcript{
		Mode:      cfg.Mode,
		SessionID: hello.SessionID,
		Initiator: peer,
		Responder: party.ID,
		X:         x,
		Y:         y,
	}
	digest, err := transcript.Digest()
	if err != nil {
		return nil, nil, err
	}
	shared, err := ephemeral.SharedPoint(x)
	if err != nil {
		return nil, nil, err
	}
	keys, err := DeriveKeySchedule(shared, digest[:])
	if err != nil {
		return nil, nil, err
	}
	if err := party.Sessions.Reserve(hello.SessionID); err != nil {
		return nil, nil, err
	}
	response := &Response{
		SessionID: append([]byte(nil), hello.SessionID...),
		Responder: party.ID.Name,
		Y:         y,
		Signature: party.sign(digest[:]),
	}
	responder := &Responder{
		party:     party,
		mode:      cfg.Mode,
		peer:      cloneIdentity(peer),
		ephemeral: ephemeral,
		hello:     cloneHello(hello),
		response:  cloneResponse(response),
		x:         x,
		y:         y,
		digest:    digest,
		keys:      keys,
		samePoint: bytes.Equal(x, y),
	}
	return responder, response, nil
}

// Response returns a defensive copy of B's response.
func (r *Responder) Response() *Response {
	if r == nil {
		return nil
	}
	return cloneResponse(r.response)
}

// Peer returns the identity B recorded for the initiator.
func (r *Responder) Peer() (Identity, bool) {
	if r == nil || r.peer.Name == "" {
		return Identity{}, false
	}
	return cloneIdentity(r.peer), true
}

// HandleInitiatorAuth verifies A's signature over the transcript digest. The
// name inside the auth message must match the identity B resolved from the
// hello; messages cannot substitute a new key or a different name at this step.
func (r *Responder) HandleInitiatorAuth(auth *InitiatorAuth) error {
	if r == nil || r.hello == nil {
		return ErrHandshakeState
	}
	if r.authVerified {
		return fmt.Errorf("%w: initiator auth already processed", ErrHandshakeState)
	}
	if auth == nil {
		return fmt.Errorf("%w: nil initiator auth", ErrHandshakeState)
	}
	if !bytes.Equal(auth.SessionID, r.hello.SessionID) {
		return fmt.Errorf("%w: auth session ID mismatch", ErrHandshakeState)
	}
	if auth.Initiator != r.peer.Name {
		return fmt.Errorf("%w: expected initiator %q, got %q", ErrIdentityMismatch, r.peer.Name, auth.Initiator)
	}
	if len(auth.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(r.peer.PublicKey, r.digest[:], auth.Signature) {
		return fmt.Errorf("%w: initiator %q", ErrSignature, r.peer.Name)
	}
	r.authVerified = true
	return nil
}

// HandleInitiatorConfirm verifies A's confirmation. A's signature must already
// have been accepted.
func (r *Responder) HandleInitiatorConfirm(confirmation *Confirmation) error {
	if r == nil || !r.authVerified {
		return fmt.Errorf("%w: initiator auth not verified", ErrHandshakeState)
	}
	if r.confirmAVerified {
		return fmt.Errorf("%w: initiator confirmation already processed", ErrHandshakeState)
	}
	if confirmation == nil {
		return fmt.Errorf("%w: nil confirmation", ErrHandshakeState)
	}
	if !bytes.Equal(confirmation.SessionID, r.hello.SessionID) {
		return fmt.Errorf("%w: confirmation session ID mismatch", ErrHandshakeState)
	}
	if confirmation.Role != RoleA {
		return fmt.Errorf("%w: expected role A, got %v", ErrConfirmation, confirmation.Role)
	}
	expected := confirmationMAC(r.keys.Confirm, RoleA, r.digest)
	if !hmacEqual(expected, confirmation.MAC) {
		return ErrConfirmation
	}
	r.confirmAVerified = true
	return nil
}

// SendResponderConfirm returns B's confirmation. It requires A's confirmation
// first, so the confirmation exchange has a fixed direction.
func (r *Responder) SendResponderConfirm() (*Confirmation, error) {
	if r == nil || !r.confirmAVerified {
		return nil, fmt.Errorf("%w: initiator confirmation not verified", ErrHandshakeState)
	}
	if r.confirmBSent {
		return nil, fmt.Errorf("%w: responder confirmation already sent", ErrHandshakeState)
	}
	confirmation := &Confirmation{
		SessionID: append([]byte(nil), r.hello.SessionID...),
		Role:      RoleB,
		MAC:       confirmationMAC(r.keys.Confirm, RoleB, r.digest),
	}
	r.confirmBSent = true
	return confirmation, nil
}

// Keys releases B's traffic keys only after B has verified A's confirmation
// and sent its own.
func (r *Responder) Keys() (KeySchedule, error) {
	if r == nil || !r.confirmBSent {
		return KeySchedule{}, fmt.Errorf("%w: responder confirmation not sent", ErrHandshakeState)
	}
	return r.keys, nil
}

// HandshakeHash returns B's transcript digest.
func (r *Responder) HandshakeHash() [32]byte {
	if r == nil {
		return [32]byte{}
	}
	return r.digest
}

// SamePoint reports whether X == Y for this session.
func (r *Responder) SamePoint() bool { return r != nil && r.samePoint }

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for index := range a {
		diff |= a[index] ^ b[index]
	}
	return diff == 0
}

// RunHandshake executes an in-memory handshake between two hosts and returns
// the verified key schedule. It is useful for tests and protocol hosts that do
// not need to serialize messages.
func RunHandshake(a, b *Party, sessionID []byte, mode Mode) (*KeySchedule, error) {
	if a == nil || b == nil {
		return nil, errors.New("netsec: nil party")
	}
	if a.ID.Name == b.ID.Name {
		return nil, errors.New("netsec: initiator and responder must differ")
	}
	initiator, hello, err := NewInitiator(a, InitiatorConfig{
		Mode: mode, SessionID: sessionID, Peer: b.ID.Name,
	})
	if err != nil {
		return nil, err
	}
	responder, response, err := NewResponder(b, ResponderConfig{Mode: mode}, hello)
	if err != nil {
		return nil, err
	}
	auth, err := initiator.HandleResponse(response)
	if err != nil {
		return nil, err
	}
	if err := responder.HandleInitiatorAuth(auth); err != nil {
		return nil, err
	}
	confirmA, err := initiator.SendConfirmA()
	if err != nil {
		return nil, err
	}
	if err := responder.HandleInitiatorConfirm(confirmA); err != nil {
		return nil, err
	}
	confirmB, err := responder.SendResponderConfirm()
	if err != nil {
		return nil, err
	}
	if err := initiator.VerifyResponderConfirm(confirmB); err != nil {
		return nil, err
	}
	aKeys, err := initiator.Keys()
	if err != nil {
		return nil, err
	}
	bKeys, err := responder.Keys()
	if err != nil {
		return nil, err
	}
	if !aKeys.Equal(bKeys) {
		return nil, errors.New("netsec: key schedule mismatch")
	}
	hashA, err := initiator.HandshakeHash()
	if err != nil {
		return nil, err
	}
	hashB := responder.HandshakeHash()
	if !bytes.Equal(hashA[:], hashB[:]) {
		return nil, errors.New("netsec: transcript digest mismatch")
	}
	return &aKeys, nil
}

// Digest is a small helper used by diagnostic output.
func (k KeySchedule) Digest() [32]byte {
	encoded := append([]byte(nil), k.SendA[:]...)
	encoded = append(encoded, k.SendB[:]...)
	encoded = append(encoded, k.Confirm[:]...)
	return sha256.Sum256(encoded)
}
