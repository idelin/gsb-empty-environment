package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"sync"
)

const (
	// DLEQLabel is the fixed domain separator used by the DLEQ challenge.
	DLEQLabel = "netsec/q0059/p256-verifiable-blind-evaluation/dleq/v1"

	// CompletionLabel is the fixed domain separator used by the final digest.
	CompletionLabel = "netsec/q0059/p256-verifiable-blind-evaluation/complete/v1"
)

const (
	compressedPointLen = 33
	scalarLen          = 32
	maxScalarAttempts  = 1024
)

var (
	// ErrStateConsumed is returned when a one-time client state is used more
	// than once. A state is consumed even if the first response is invalid.
	ErrStateConsumed = errors.New("netsec: client state already consumed")

	// ErrInvalidPoint means a value is not a canonical, finite P-256 point.
	ErrInvalidPoint = errors.New("netsec: invalid P-256 point")

	// ErrInvalidScalar means a scalar is nil, negative, or not less than q.
	ErrInvalidScalar = errors.New("netsec: invalid P-256 scalar")

	// ErrProofRejected means the DLEQ proof did not verify.
	ErrProofRejected = errors.New("netsec: DLEQ proof rejected")

	// ErrRandomness means the host-provided random source failed to produce a
	// usable scalar.
	ErrRandomness = errors.New("netsec: host random source failed")
)

var p256 = elliptic.P256()
var p256Params = p256.Params()
var p256Q = new(big.Int).Set(p256Params.N)

// HashToGroupFunc is the host boundary for mapping the query input to a group
// element. The returned point must be a canonical non-infinity P-256 point.
// Hosts that need domain separation can close over their context or use a
// ContextHashToGroupFunc with NewClientWithContext.
type HashToGroupFunc func(input []byte) (*ecdsa.PublicKey, error)

// ContextHashToGroupFunc is an optional host boundary that receives both the
// input and the host context.
type ContextHashToGroupFunc func(input, context []byte) (*ecdsa.PublicKey, error)

// HashToGroup and ContextHashToGroup are short aliases for the host function
// types.
type HashToGroup = HashToGroupFunc
type ContextHashToGroup = ContextHashToGroupFunc

// DLEQProof is the Schnorr-style proof (c, s).
type DLEQProof struct {
	C *big.Int
	S *big.Int
}

// BlindRequest is what the client sends to the server. It deliberately has no
// public-key field: the server always uses the key it was constructed with.
type BlindRequest struct {
	B *ecdsa.PublicKey
}

// BlindResponse is the server's evaluation E=kB together with its DLEQ proof.
// The proof is embedded so both resp.C and resp.Proof.C are available.
type BlindResponse struct {
	E *ecdsa.PublicKey
	DLEQProof
}

// Request and Response are short aliases for callers that prefer them.
type Request = BlindRequest
type Response = BlindResponse
type ClientRequest = BlindRequest
type ServerResponse = BlindResponse

// Client holds only host-provided dependencies and the trusted server public
// key. It never learns the server's private key.
type Client struct {
	hashToGroup        HashToGroupFunc
	contextHashToGroup ContextHashToGroupFunc
	randomness         io.Reader
	serverPublicKey    *ecdsa.PublicKey

	hashMu sync.Mutex
	randMu sync.Mutex
}

// NewClient constructs a client. hashToGroup is a trusted host primitive,
// randomness is the host's cryptographically secure random source, and
// serverPublicKey is the separately provisioned trusted public key K. The
// hasher may have either HashToGroupFunc or ContextHashToGroupFunc signature.
func NewClient(hashToGroup any, randomness io.Reader, serverPublicKey *ecdsa.PublicKey) (*Client, error) {
	switch hasher := hashToGroup.(type) {
	case HashToGroupFunc:
		return newClient(hasher, nil, randomness, serverPublicKey)
	case func([]byte) (*ecdsa.PublicKey, error):
		return newClient(HashToGroupFunc(hasher), nil, randomness, serverPublicKey)
	case ContextHashToGroupFunc:
		return newClient(nil, hasher, randomness, serverPublicKey)
	case func([]byte, []byte) (*ecdsa.PublicKey, error):
		return newClient(nil, ContextHashToGroupFunc(hasher), randomness, serverPublicKey)
	case interface {
		HashToGroup([]byte) (*ecdsa.PublicKey, error)
	}:
		return newClient(hasher.HashToGroup, nil, randomness, serverPublicKey)
	case interface {
		HashToGroup([]byte, []byte) (*ecdsa.PublicKey, error)
	}:
		return newClient(nil, hasher.HashToGroup, randomness, serverPublicKey)
	case nil:
		return nil, errors.New("netsec: HashToGroup function is nil")
	default:
		return nil, fmt.Errorf("netsec: unsupported HashToGroup type %T", hashToGroup)
	}
}

// NewClientWithContext is like NewClient but allows HashToGroup to receive the
// host context in addition to the original input.
func NewClientWithContext(hashToGroup ContextHashToGroupFunc, randomness io.Reader, serverPublicKey *ecdsa.PublicKey) (*Client, error) {
	return newClient(nil, hashToGroup, randomness, serverPublicKey)
}

func newClient(hashToGroup HashToGroupFunc, contextHashToGroup ContextHashToGroupFunc, randomness io.Reader, serverPublicKey *ecdsa.PublicKey) (*Client, error) {
	if hashToGroup == nil && contextHashToGroup == nil {
		return nil, errors.New("netsec: HashToGroup function is nil")
	}
	if randomness == nil {
		return nil, errors.New("netsec: random source is nil")
	}
	if err := validatePoint(serverPublicKey); err != nil {
		return nil, fmt.Errorf("trusted server public key: %w", err)
	}
	return &Client{
		hashToGroup:        hashToGroup,
		contextHashToGroup: contextHashToGroup,
		randomness:         randomness,
		serverPublicKey:    clonePoint(serverPublicKey),
	}, nil
}

// Blind maps the input to P, samples a nonzero blind r, and returns B=rP
// together with a fresh one-time state. No state or request is returned if any
// step fails, including a random-source failure.
func (c *Client) Blind(input, context []byte) (*BlindRequest, *ClientState, error) {
	if c == nil {
		return nil, nil, errors.New("netsec: nil client")
	}

	c.hashMu.Lock()
	var point *ecdsa.PublicKey
	var err error
	if c.contextHashToGroup != nil {
		point, err = c.contextHashToGroup(append([]byte(nil), input...), append([]byte(nil), context...))
	} else {
		point, err = c.hashToGroup(append([]byte(nil), input...))
	}
	c.hashMu.Unlock()
	if err != nil {
		return nil, nil, fmt.Errorf("HashToGroup: %w", err)
	}
	if err := validatePoint(point); err != nil {
		return nil, nil, fmt.Errorf("HashToGroup result: %w", err)
	}
	point = clonePoint(point)

	c.randMu.Lock()
	r, err := sampleNonzeroScalar(c.randomness)
	c.randMu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	x, y := p256.ScalarMult(point.X, point.Y, r.Bytes())
	blindPoint, err := pointFromCoordinates(x, y)
	if err != nil {
		return nil, nil, fmt.Errorf("blind point: %w", err)
	}

	request := &BlindRequest{B: clonePoint(blindPoint)}
	state := &ClientState{core: &clientStateCore{
		r:               new(big.Int).Set(r),
		blindPoint:      clonePoint(blindPoint),
		context:         append([]byte(nil), context...),
		input:           append([]byte(nil), input...),
		serverPublicKey: clonePoint(c.serverPublicKey),
	}}
	return request, state, nil
}

// ClientState is the one-time state saved between Blind and Complete. Its
// mutable contents live behind a pointer, so copying the struct does not
// create a second usable state.
type ClientState struct {
	core *clientStateCore
}

type clientStateCore struct {
	mu sync.Mutex

	consumed        bool
	r               *big.Int
	blindPoint      *ecdsa.PublicKey
	context         []byte
	input           []byte
	serverPublicKey *ecdsa.PublicKey
}

// State is a shorter alias for ClientState.
type State = ClientState

// BlindClient and BlindServer are descriptive aliases.
type BlindClient = Client
type BlindServer = Server

// Complete consumes the state, verifies E and its DLEQ proof against the
// stored B, context, and trusted K, computes Z=r^-1E, and returns the final
// completed-query digest. The state remains consumed after either success or
// failure.
func (s *ClientState) Complete(response *BlindResponse) ([]byte, error) {
	if s == nil || s.core == nil {
		return nil, errors.New("netsec: nil client state")
	}

	core := s.core
	core.mu.Lock()
	if core.consumed {
		core.mu.Unlock()
		return nil, ErrStateConsumed
	}
	core.consumed = true

	r := new(big.Int).Set(core.r)
	b := clonePoint(core.blindPoint)
	context := append([]byte(nil), core.context...)
	input := append([]byte(nil), core.input...)
	serverPublicKey := clonePoint(core.serverPublicKey)
	core.r = nil
	core.blindPoint = nil
	core.context = nil
	core.input = nil
	core.serverPublicKey = nil
	core.mu.Unlock()

	if response == nil || response.E == nil {
		return nil, fmt.Errorf("%w: missing evaluation point", ErrProofRejected)
	}
	evaluatedPoint := clonePoint(response.E)
	if err := validatePoint(evaluatedPoint); err != nil {
		return nil, fmt.Errorf("%w: evaluation point: %v", ErrProofRejected, err)
	}
	proof, err := canonicalProof(response.DLEQProof)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProofRejected, err)
	}
	if err := verifyDLEQ(serverPublicKey, b, evaluatedPoint, proof, context); err != nil {
		return nil, err
	}

	rInverse := new(big.Int).ModInverse(r, p256Q)
	if rInverse == nil {
		return nil, fmt.Errorf("%w: blind scalar is not invertible", ErrProofRejected)
	}
	x, y := p256.ScalarMult(evaluatedPoint.X, evaluatedPoint.Y, rInverse.Bytes())
	z, err := pointFromCoordinates(x, y)
	if err != nil {
		return nil, fmt.Errorf("%w: unblinded point: %v", ErrProofRejected, err)
	}

	return completionDigest(context, input, z)
}

// Verify is an alias for Complete.
func (s *ClientState) Verify(response *BlindResponse) ([]byte, error) {
	return s.Complete(response)
}

// Finalize is an alias for Complete.
func (s *ClientState) Finalize(response *BlindResponse) ([]byte, error) {
	return s.Complete(response)
}

// Server holds a private scalar and the matching public key. PublicKey is a
// convenience snapshot; evaluation always uses the internally derived key.
type Server struct {
	PublicKey *ecdsa.PublicKey

	privateScalar *big.Int
	publicKey     *ecdsa.PublicKey
	randomness    io.Reader
	randMu        sync.Mutex
}

// NewServer constructs a server from either an *ecdsa.PrivateKey or a private
// scalar accepted by *big.Int. Its public key is always recomputed as K=kG
// rather than copied from any supplied key object.
func NewServer(privateKey any, randomness io.Reader) (*Server, error) {
	switch key := privateKey.(type) {
	case *ecdsa.PrivateKey:
		if key == nil || key.D == nil {
			return nil, errors.New("netsec: server private key is nil")
		}
		if err := validatePrivateKeyCurve(key.Curve); err != nil {
			return nil, err
		}
		return NewServerFromScalar(key.D, randomness)
	case ecdsa.PrivateKey:
		if key.D == nil {
			return nil, errors.New("netsec: server private key is nil")
		}
		if err := validatePrivateKeyCurve(key.Curve); err != nil {
			return nil, err
		}
		return NewServerFromScalar(key.D, randomness)
	case *big.Int:
		return NewServerFromScalar(key, randomness)
	case big.Int:
		return NewServerFromScalar(&key, randomness)
	case nil:
		return nil, errors.New("netsec: server private key is nil")
	default:
		return nil, fmt.Errorf("netsec: unsupported server private-key type %T", privateKey)
	}
}

// NewServerFromPrivateKey is an alias for NewServer.
func NewServerFromPrivateKey(privateKey *ecdsa.PrivateKey, randomness io.Reader) (*Server, error) {
	if privateKey == nil || privateKey.D == nil {
		return nil, errors.New("netsec: server private key is nil")
	}
	return NewServerFromScalar(privateKey.D, randomness)
}

// NewServerFromScalar constructs a server from a private scalar k.
func NewServerFromScalar(k *big.Int, randomness io.Reader) (*Server, error) {
	if randomness == nil {
		return nil, errors.New("netsec: random source is nil")
	}
	if err := validatePrivateScalar(k); err != nil {
		return nil, fmt.Errorf("server private key: %w", err)
	}
	k = new(big.Int).Set(k)
	x, y := p256.ScalarBaseMult(k.Bytes())
	publicKey, err := pointFromCoordinates(x, y)
	if err != nil {
		return nil, fmt.Errorf("server public key: %w", err)
	}
	return &Server{
		PublicKey:     clonePoint(publicKey),
		privateScalar: k,
		publicKey:     clonePoint(publicKey),
		randomness:    randomness,
	}, nil
}

// Key returns a defensive copy of K=kG.
func (s *Server) Key() *ecdsa.PublicKey {
	if s == nil || s.publicKey == nil {
		return nil
	}
	return clonePoint(s.publicKey)
}

// K is an alias for Key.
func (s *Server) K() *ecdsa.PublicKey {
	return s.Key()
}

// Evaluate validates B, samples t, computes E=kB and A=tG, D=tB, and returns
// a DLEQ proof bound to the trusted server key, B, E, and the host context.
func (s *Server) Evaluate(request *BlindRequest, context []byte) (*BlindResponse, error) {
	if s == nil || s.privateScalar == nil || s.publicKey == nil {
		return nil, errors.New("netsec: uninitialized server")
	}
	if request == nil {
		return nil, errors.New("netsec: nil blind request")
	}
	if err := validatePoint(request.B); err != nil {
		return nil, fmt.Errorf("blind request point: %w", err)
	}
	b := clonePoint(request.B)

	x, y := p256.ScalarMult(b.X, b.Y, s.privateScalar.Bytes())
	e, err := pointFromCoordinates(x, y)
	if err != nil {
		return nil, fmt.Errorf("evaluation point: %w", err)
	}

	s.randMu.Lock()
	t, err := sampleNonzeroScalar(s.randomness)
	s.randMu.Unlock()
	if err != nil {
		return nil, err
	}

	ax, ay := p256.ScalarBaseMult(t.Bytes())
	a, err := pointFromCoordinates(ax, ay)
	if err != nil {
		return nil, fmt.Errorf("proof point A: %w", err)
	}
	dx, dy := p256.ScalarMult(b.X, b.Y, t.Bytes())
	d, err := pointFromCoordinates(dx, dy)
	if err != nil {
		return nil, fmt.Errorf("proof point D: %w", err)
	}

	c, err := ComputeDLEQChallenge(context, s.publicKey, b, e, a, d)
	if err != nil {
		return nil, err
	}
	sValue := new(big.Int).Mul(c, s.privateScalar)
	sValue.Sub(t, sValue)
	sValue.Mod(sValue, p256Q)

	return &BlindResponse{
		E: clonePoint(e),
		DLEQProof: DLEQProof{
			C: new(big.Int).Set(c),
			S: sValue,
		},
	}, nil
}

// ComputeDLEQChallenge computes
//
//	SHA256(domain, context, K, B, E, A, D) mod q
//
// where every field is length-prefixed with a four-byte big-endian length and
// every point is encoded as a canonical 33-byte compressed SEC1 point.
func ComputeDLEQChallenge(context []byte, publicKey, blindPoint, evaluatedPoint, a, d *ecdsa.PublicKey) (*big.Int, error) {
	if uint64(len(context)) > uint64(^uint32(0)) {
		return nil, errors.New("netsec: context is too long")
	}
	for name, point := range map[string]*ecdsa.PublicKey{
		"K": publicKey,
		"B": blindPoint,
		"E": evaluatedPoint,
		"A": a,
		"D": d,
	} {
		if err := validatePoint(point); err != nil {
			return nil, fmt.Errorf("DLEQ challenge point %s: %w", name, err)
		}
	}

	transcript := sha256.New()
	writeField(transcript, []byte(DLEQLabel))
	writeField(transcript, context)
	for _, point := range []*ecdsa.PublicKey{publicKey, blindPoint, evaluatedPoint, a, d} {
		encoded, err := encodePoint(point)
		if err != nil {
			return nil, err
		}
		writeField(transcript, encoded)
	}

	digest := transcript.Sum(nil)
	return new(big.Int).Mod(new(big.Int).SetBytes(digest), p256Q), nil
}

// VerifyDLEQ checks a proof (c,s) against K, B, and E. It reconstructs
// A=sG+cK and D=sB+cE, then recomputes the challenge.
func VerifyDLEQ(publicKey, blindPoint, evaluatedPoint *ecdsa.PublicKey, c, s *big.Int, context []byte) error {
	if err := validatePoint(publicKey); err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	if err := validatePoint(blindPoint); err != nil {
		return fmt.Errorf("blind point: %w", err)
	}
	if err := validatePoint(evaluatedPoint); err != nil {
		return fmt.Errorf("evaluation point: %w", err)
	}
	if err := validateProofScalar(c, "c"); err != nil {
		return err
	}
	if err := validateProofScalar(s, "s"); err != nil {
		return err
	}
	return verifyDLEQ(publicKey, blindPoint, evaluatedPoint, DLEQProof{
		C: new(big.Int).Set(c),
		S: new(big.Int).Set(s),
	}, context)
}

// VerifyDLEQProof is a convenience wrapper around VerifyDLEQ.
func VerifyDLEQProof(publicKey, blindPoint, evaluatedPoint *ecdsa.PublicKey, proof DLEQProof, context []byte) error {
	return VerifyDLEQ(publicKey, blindPoint, evaluatedPoint, proof.C, proof.S, context)
}

func verifyDLEQ(publicKey, blindPoint, evaluatedPoint *ecdsa.PublicKey, proof DLEQProof, context []byte) error {
	for name, point := range map[string]*ecdsa.PublicKey{
		"K": publicKey,
		"B": blindPoint,
		"E": evaluatedPoint,
	} {
		if err := validatePoint(point); err != nil {
			return fmt.Errorf("%w: point %s: %v", ErrProofRejected, name, err)
		}
	}
	if err := validateProofScalar(proof.C, "c"); err != nil {
		return fmt.Errorf("%w: %v", ErrProofRejected, err)
	}
	if err := validateProofScalar(proof.S, "s"); err != nil {
		return fmt.Errorf("%w: %v", ErrProofRejected, err)
	}

	sG := baseMult(proof.S)
	cK := scalarMult(publicKey, proof.C)
	a := addPoints(sG, cK)
	if a == nil {
		return fmt.Errorf("%w: reconstructed A is infinity", ErrProofRejected)
	}

	sB := scalarMult(blindPoint, proof.S)
	cE := scalarMult(evaluatedPoint, proof.C)
	d := addPoints(sB, cE)
	if d == nil {
		return fmt.Errorf("%w: reconstructed D is infinity", ErrProofRejected)
	}

	expected, err := ComputeDLEQChallenge(context, publicKey, blindPoint, evaluatedPoint, a, d)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProofRejected, err)
	}
	if expected.Cmp(proof.C) != 0 {
		return ErrProofRejected
	}
	return nil
}

// completionDigest computes SHA256(completion label || context || input ||
// canonical compressed Z), exactly as specified by the protocol.
func completionDigest(context, input []byte, z *ecdsa.PublicKey) ([]byte, error) {
	encodedZ, err := encodePoint(z)
	if err != nil {
		return nil, err
	}
	transcript := sha256.New()
	transcript.Write([]byte(CompletionLabel))
	transcript.Write(context)
	transcript.Write(input)
	transcript.Write(encodedZ)
	return transcript.Sum(nil), nil
}

func writeField(h hash.Hash, field []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(field)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(field)
}

func encodePoint(point *ecdsa.PublicKey) ([]byte, error) {
	if err := validatePoint(point); err != nil {
		return nil, err
	}
	encoded := elliptic.MarshalCompressed(p256, point.X, point.Y)
	if len(encoded) != compressedPointLen {
		return nil, fmt.Errorf("%w: unexpected compressed length", ErrInvalidPoint)
	}
	decodedX, decodedY := elliptic.UnmarshalCompressed(p256, encoded)
	if decodedX == nil || decodedY == nil || decodedX.Cmp(point.X) != 0 || decodedY.Cmp(point.Y) != 0 {
		return nil, fmt.Errorf("%w: non-canonical compressed form", ErrInvalidPoint)
	}
	return encoded, nil
}

func validatePoint(point *ecdsa.PublicKey) error {
	if point == nil || point.Curve == nil || point.X == nil || point.Y == nil {
		return ErrInvalidPoint
	}
	params := point.Curve.Params()
	if params == nil ||
		params.P == nil || params.N == nil || params.B == nil ||
		params.Gx == nil || params.Gy == nil ||
		params.P.Cmp(p256Params.P) != 0 ||
		params.N.Cmp(p256Params.N) != 0 ||
		params.B.Cmp(p256Params.B) != 0 ||
		params.Gx.Cmp(p256Params.Gx) != 0 ||
		params.Gy.Cmp(p256Params.Gy) != 0 {
		return fmt.Errorf("%w: not the P-256 curve", ErrInvalidPoint)
	}
	if point.X.Sign() < 0 || point.X.Cmp(p256Params.P) >= 0 ||
		point.Y.Sign() < 0 || point.Y.Cmp(p256Params.P) >= 0 {
		return fmt.Errorf("%w: coordinates are not canonical", ErrInvalidPoint)
	}
	if !p256.IsOnCurve(point.X, point.Y) {
		return fmt.Errorf("%w: point is not on P-256", ErrInvalidPoint)
	}
	return nil
}

func pointFromCoordinates(x, y *big.Int) (*ecdsa.PublicKey, error) {
	if x == nil || y == nil {
		return nil, ErrInvalidPoint
	}
	point := &ecdsa.PublicKey{
		Curve: p256,
		X:     new(big.Int).Set(x),
		Y:     new(big.Int).Set(y),
	}
	if err := validatePoint(point); err != nil {
		return nil, err
	}
	return point, nil
}

func clonePoint(point *ecdsa.PublicKey) *ecdsa.PublicKey {
	if point == nil {
		return nil
	}
	cloned := &ecdsa.PublicKey{Curve: point.Curve}
	if point.X != nil {
		cloned.X = new(big.Int).Set(point.X)
	}
	if point.Y != nil {
		cloned.Y = new(big.Int).Set(point.Y)
	}
	return cloned
}

func baseMult(scalar *big.Int) *ecdsa.PublicKey {
	if scalar == nil || scalar.Sign() == 0 {
		return nil
	}
	x, y := p256.ScalarBaseMult(scalar.Bytes())
	if x == nil || y == nil {
		return nil
	}
	return &ecdsa.PublicKey{Curve: p256, X: new(big.Int).Set(x), Y: new(big.Int).Set(y)}
}

func scalarMult(point *ecdsa.PublicKey, scalar *big.Int) *ecdsa.PublicKey {
	if point == nil || scalar == nil || scalar.Sign() == 0 {
		return nil
	}
	x, y := p256.ScalarMult(point.X, point.Y, scalar.Bytes())
	if x == nil || y == nil {
		return nil
	}
	return &ecdsa.PublicKey{Curve: p256, X: new(big.Int).Set(x), Y: new(big.Int).Set(y)}
}

func addPoints(left, right *ecdsa.PublicKey) *ecdsa.PublicKey {
	switch {
	case left == nil && right == nil:
		return nil
	case left == nil:
		return clonePoint(right)
	case right == nil:
		return clonePoint(left)
	}
	x, y := p256.Add(left.X, left.Y, right.X, right.Y)
	if x == nil || y == nil {
		return nil
	}
	return &ecdsa.PublicKey{Curve: p256, X: new(big.Int).Set(x), Y: new(big.Int).Set(y)}
}

func validatePrivateScalar(scalar *big.Int) error {
	if scalar == nil || scalar.Sign() <= 0 || scalar.Cmp(p256Q) >= 0 {
		return fmt.Errorf("%w: private scalar must satisfy 1 <= k < q", ErrInvalidScalar)
	}
	return nil
}

func validatePrivateKeyCurve(curve elliptic.Curve) error {
	if curve == nil {
		return nil
	}
	params := curve.Params()
	if params == nil ||
		params.P == nil || params.N == nil || params.B == nil ||
		params.Gx == nil || params.Gy == nil ||
		params.P.Cmp(p256Params.P) != 0 ||
		params.N.Cmp(p256Params.N) != 0 ||
		params.B.Cmp(p256Params.B) != 0 ||
		params.Gx.Cmp(p256Params.Gx) != 0 ||
		params.Gy.Cmp(p256Params.Gy) != 0 {
		return fmt.Errorf("%w: server private key is not on P-256", ErrInvalidPoint)
	}
	return nil
}

func validateProofScalar(scalar *big.Int, name string) error {
	if scalar == nil || scalar.Sign() < 0 || scalar.Cmp(p256Q) >= 0 {
		return fmt.Errorf("%w: %s must be canonical and satisfy 0 <= %s < q", ErrInvalidScalar, name, name)
	}
	return nil
}

func canonicalProof(proof DLEQProof) (DLEQProof, error) {
	if err := validateProofScalar(proof.C, "c"); err != nil {
		return DLEQProof{}, err
	}
	if err := validateProofScalar(proof.S, "s"); err != nil {
		return DLEQProof{}, err
	}
	return DLEQProof{
		C: new(big.Int).Set(proof.C),
		S: new(big.Int).Set(proof.S),
	}, nil
}

// sampleNonzeroScalar performs rejection sampling from the host source. It
// never substitutes another source; a source failure or persistently unusable
// output is reported to the caller.
func sampleNonzeroScalar(randomness io.Reader) (*big.Int, error) {
	var raw [scalarLen]byte
	for i := 0; i < maxScalarAttempts; i++ {
		if err := readFull(randomness, raw[:]); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRandomness, err)
		}
		scalar := new(big.Int).SetBytes(raw[:])
		if scalar.Sign() > 0 && scalar.Cmp(p256Q) < 0 {
			return scalar, nil
		}
	}
	return nil, fmt.Errorf("%w: source did not yield a scalar in [1,q)", ErrRandomness)
}

func readFull(randomness io.Reader, dst []byte) error {
	const maxEmptyReads = 100
	emptyReads := 0
	for offset := 0; offset < len(dst); {
		n, err := randomness.Read(dst[offset:])
		if n < 0 || n > len(dst)-offset {
			return errors.New("random source returned an invalid byte count")
		}
		offset += n
		if offset == len(dst) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			emptyReads++
			if emptyReads >= maxEmptyReads {
				return io.ErrNoProgress
			}
			continue
		}
		emptyReads = 0
	}
	return nil
}
