package pathsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// KeyAlgorithm identifies a BGPsec signature algorithm.
type KeyAlgorithm string

const (
	AlgorithmEd25519         KeyAlgorithm = "ed25519"
	AlgorithmECDSAP256       KeyAlgorithm = "ecdsa-p256"
	AlgorithmECDSAP256SHA256 KeyAlgorithm = "ecdsa-p256-sha256"

	// RFC 8205 suite identifiers used by RFC8205SigningBytes.
	SuiteECDSAP256SHA256 uint16 = 1
	SuiteEd25519         uint16 = 2
)

// TrustedKey is a trusted BGPsec router public key.
type TrustedKey struct {
	ASN       uint32
	KeyID     []byte
	SKI       []byte // alias commonly used for Subject Key Identifier
	Algorithm KeyAlgorithm
	PublicKey any
	NotBefore time.Time
	ExpiresAt time.Time
}

func (k TrustedKey) id() []byte {
	if len(k.KeyID) > 0 {
		return k.KeyID
	}
	return k.SKI
}

// KeyStore is an AS-indexed trusted router public key table.
type KeyStore struct {
	keys map[uint32][]TrustedKey
	now  func() time.Time
}

// PublicKeyTable is a descriptive alias for the trusted router key table.
type PublicKeyTable = KeyStore

// NewKeyStore creates an empty trusted key table.
func NewKeyStore() *KeyStore {
	return &KeyStore{keys: make(map[uint32][]TrustedKey), now: time.Now}
}

// NewPublicKeyTable creates a trusted BGPsec public key table.
func NewPublicKeyTable() *PublicKeyTable { return NewKeyStore() }

// AddPublicKey trusts an Ed25519 or ECDSA-P256 key and infers its algorithm.
func (s *KeyStore) AddPublicKey(asn uint32, publicKey any, keyID ...[]byte) error {
	key := TrustedKey{ASN: asn, PublicKey: publicKey}
	if len(keyID) > 0 {
		key.KeyID = keyID[0]
	}
	return s.AddKey(key)
}

// Trust is a panic-on-error alias for AddPublicKey.
func (s *KeyStore) Trust(asn uint32, publicKey any, keyID ...[]byte) *KeyStore {
	if err := s.AddPublicKey(asn, publicKey, keyID...); err != nil {
		panic(err)
	}
	return s
}

// SetClock replaces the validity clock.
func (s *KeyStore) SetClock(now func() time.Time) *KeyStore {
	s.now = now
	return s
}

func (s *KeyStore) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// AddKey stores a trusted key after validating its concrete type.
func (s *KeyStore) AddKey(key TrustedKey) error {
	alg, pub, err := normalizePublicKey(key.PublicKey, key.Algorithm)
	if err != nil {
		return err
	}
	key.Algorithm = alg
	key.PublicKey = pub
	if len(key.KeyID) == 0 && len(key.SKI) > 0 {
		key.KeyID = append([]byte(nil), key.SKI...)
	}
	if len(key.KeyID) == 0 {
		key.KeyID = defaultKeyID(pub)
	}
	key.KeyID = append([]byte(nil), key.KeyID...)
	key.SKI = append([]byte(nil), key.KeyID...)
	if s.keys == nil {
		s.keys = make(map[uint32][]TrustedKey)
	}
	s.keys[key.ASN] = append(s.keys[key.ASN], key)
	return nil
}

// AddEd25519 trusts an Ed25519 public key for asn.
func (s *KeyStore) AddEd25519(asn uint32, publicKey ed25519.PublicKey, keyID ...[]byte) error {
	key := TrustedKey{ASN: asn, Algorithm: AlgorithmEd25519, PublicKey: publicKey}
	if len(keyID) > 0 {
		key.KeyID = keyID[0]
	}
	return s.AddKey(key)
}

// TrustEd25519 is a panic-on-error fixture helper.
func (s *KeyStore) TrustEd25519(asn uint32, publicKey ed25519.PublicKey, keyID ...[]byte) *KeyStore {
	if err := s.AddEd25519(asn, publicKey, keyID...); err != nil {
		panic(err)
	}
	return s
}

// AddECDSAP256 trusts an ECDSA-P256 public key for asn.
func (s *KeyStore) AddECDSAP256(asn uint32, publicKey *ecdsa.PublicKey, keyID ...[]byte) error {
	key := TrustedKey{ASN: asn, Algorithm: AlgorithmECDSAP256SHA256, PublicKey: publicKey}
	if len(keyID) > 0 {
		key.KeyID = keyID[0]
	}
	return s.AddKey(key)
}

// TrustECDSAP256 is a panic-on-error fixture helper.
func (s *KeyStore) TrustECDSAP256(asn uint32, publicKey *ecdsa.PublicKey, keyID ...[]byte) *KeyStore {
	if err := s.AddECDSAP256(asn, publicKey, keyID...); err != nil {
		panic(err)
	}
	return s
}

// Lookup returns currently valid keys for asn, optionally matching keyID.
func (s *KeyStore) Lookup(asn uint32, keyID []byte, at ...time.Time) []TrustedKey {
	if s == nil {
		return nil
	}
	now := s.currentTime()
	if len(at) > 0 && !at[0].IsZero() {
		now = at[0]
	}
	var out []TrustedKey
	for _, key := range s.keys[asn] {
		if !key.NotBefore.IsZero() && now.Before(key.NotBefore) {
			continue
		}
		if !key.ExpiresAt.IsZero() && !now.Before(key.ExpiresAt) {
			continue
		}
		// keyID/SKI is a selection hint. All currently trusted keys for the
		// AS are allowed to verify; a wrong hint cannot grant trust because
		// the cryptographic check still has to succeed.
		_ = keyID
		out = append(out, key)
	}
	return out
}

func normalizePublicKey(pub any, alg KeyAlgorithm) (KeyAlgorithm, any, error) {
	switch key := pub.(type) {
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return alg, nil, fmt.Errorf("%w: Ed25519 key length %d", ErrBadSignature, len(key))
		}
		return AlgorithmEd25519, key, nil
	case *ecdsa.PublicKey:
		if key == nil || key.Curve == nil || key.Curve.Params().Name != "P-256" {
			return alg, nil, fmt.Errorf("%w: expected ECDSA P-256 key", ErrBadSignature)
		}
		return AlgorithmECDSAP256SHA256, key, nil
	case ecdsa.PublicKey:
		if key.Curve == nil || key.Curve.Params().Name != "P-256" {
			return alg, nil, fmt.Errorf("%w: expected ECDSA P-256 key", ErrBadSignature)
		}
		return AlgorithmECDSAP256SHA256, &key, nil
	default:
		return alg, nil, fmt.Errorf("%w: unsupported public key type %T", ErrBadSignature, pub)
	}
}

func defaultKeyID(pub any) []byte {
	sum := sha256.Sum256(marshalPublicKeyForID(pub))
	return sum[:20]
}

func marshalPublicKeyForID(pub any) []byte {
	switch key := pub.(type) {
	case ed25519.PublicKey:
		return []byte(key)
	case *ecdsa.PublicKey:
		if key == nil || key.X == nil || key.Y == nil {
			return nil
		}
		return elliptic.Marshal(key.Curve, key.X, key.Y)
	case ecdsa.PublicKey:
		return elliptic.Marshal(key.Curve, key.X, key.Y)
	default:
		return nil
	}
}

// SecurePathHop is one RFC 8205 Secure_Path segment.
type SecurePathHop struct {
	PCount    int    `json:"pCount"`
	PathCount int    `json:"pathCount,omitempty"` // alternate descriptive name
	Flags     uint8  `json:"flags,omitempty"`
	ASN       uint32 `json:"asn,omitempty"`
	AS        uint32 `json:"AS,omitempty"`
	ASNumber  uint32 `json:"asNumber,omitempty"`
	TargetASN uint32 `json:"targetASN,omitempty"`
	TargetAS  uint32 `json:"targetAS,omitempty"`
}

// HopASN returns the ASN from supported field aliases.
func (h SecurePathHop) HopASN() uint32 {
	for _, asn := range []uint32{h.ASN, h.AS, h.ASNumber} {
		if asn != 0 {
			return asn
		}
	}
	return 0
}

// HopPCount returns pCount from supported aliases.
func (h SecurePathHop) HopPCount() int {
	if h.PCount != 0 {
		return h.PCount
	}
	return h.PathCount
}

// HopTargetASN returns a hop-specific target AS alias.
func (h SecurePathHop) HopTargetASN() uint32 {
	for _, asn := range []uint32{h.TargetASN, h.TargetAS} {
		if asn != 0 {
			return asn
		}
	}
	return 0
}

// Signature is one BGPsec Signature_Block entry.
type Signature struct {
	SignerASN        uint32       `json:"signerASN,omitempty"`
	ASN              uint32       `json:"asn,omitempty"`
	KeyID            []byte       `json:"keyID,omitempty"`
	SKI              []byte       `json:"ski,omitempty"`
	Algorithm        KeyAlgorithm `json:"algorithm,omitempty"`
	AlgorithmSuiteID uint16       `json:"algorithmSuiteID,omitempty"`
	Signature        []byte       `json:"signature,omitempty"`
	Value            []byte       `json:"value,omitempty"`
	Sig              []byte       `json:"sig,omitempty"`
	TargetASN        uint32       `json:"targetASN,omitempty"`
	TargetAS         uint32       `json:"targetAS,omitempty"`
}

func (s Signature) signerASN() uint32 {
	for _, asn := range []uint32{s.SignerASN, s.ASN} {
		if asn != 0 {
			return asn
		}
	}
	return 0
}

func (s Signature) keyID() []byte {
	if len(s.KeyID) > 0 {
		return s.KeyID
	}
	return s.SKI
}

func (s Signature) value() []byte {
	for _, value := range [][]byte{s.Signature, s.Value, s.Sig} {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func (s Signature) target() uint32 {
	for _, asn := range []uint32{s.TargetASN, s.TargetAS} {
		if asn != 0 {
			return asn
		}
	}
	return 0
}

// BGPsecProof contains Secure_Path and Signature_Block data.
type BGPsecProof struct {
	SecurePath         []SecurePathHop `json:"securePath,omitempty"`
	SecurePathHops     []SecurePathHop `json:"securePathHops,omitempty"`
	SecurePathSegments []SecurePathHop `json:"securePathSegments,omitempty"`
	SignatureBlock     []Signature     `json:"signatureBlock,omitempty"`
	Signatures         []Signature     `json:"signatures,omitempty"`
	TargetASN          uint32          `json:"targetASN,omitempty"`
	TargetAS           uint32          `json:"targetAS,omitempty"`
	LocalASN           uint32          `json:"localASN,omitempty"`
}

func (p BGPsecProof) hops() []SecurePathHop {
	for _, candidate := range [][]SecurePathHop{p.SecurePath, p.SecurePathHops, p.SecurePathSegments} {
		if len(candidate) > 0 {
			return candidate
		}
	}
	return nil
}

func (p BGPsecProof) sigs() []Signature {
	for _, candidate := range [][]Signature{p.SignatureBlock, p.Signatures} {
		if len(candidate) > 0 {
			return candidate
		}
	}
	return nil
}

func (p BGPsecProof) target() uint32 {
	for _, asn := range []uint32{p.TargetASN, p.TargetAS, p.LocalASN} {
		if asn != 0 {
			return asn
		}
	}
	return 0
}

// BGPsecRequest is an isolated BGPsec verification request.
type BGPsecRequest struct {
	Prefix     string
	ASPath     []uint32
	Normalized NormalizedPath
	Proof      *BGPsecProof

	// These fields allow callers to pass Secure_Path and Signature_Block
	// directly without constructing Proof.
	SecurePath     []SecurePathHop
	SignatureBlock []Signature
	TargetASN      uint32
	LocalASN       uint32

	Now time.Time
	// PayloadBuilder allows a host to supply the exact RFC 8205 signed-byte
	// profile used by its wire encoder. The default is RFC8205SigningBytes.
	PayloadBuilder func(prefix Prefix, hop SecurePathHop, targetASN uint32, predecessor []byte, algorithm KeyAlgorithm) []byte
	// EnforceRunCounts requires pCount to equal adjacent-prepend run size.
	EnforceRunCounts bool
	// IgnorePCount simulates a weak validator that does not reject illegal
	// pCount values. Signatures are still verified over the signed value.
	IgnorePCount bool
	// RequireFinalTarget makes a missing local/target AS an Invalid result.
	RequireFinalTarget bool
}

// BGPsecHopResult explains one verified or rejected hop.
type BGPsecHopResult struct {
	Index     int    `json:"index"`
	SignerASN uint32 `json:"signerASN"`
	TargetASN uint32 `json:"targetASN"`
	PCount    int    `json:"pCount"`
	Status    Status `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// BGPsecResult is the chain verification result.
type BGPsecResult struct {
	Status       Status            `json:"status"`
	VerifiedHops int               `json:"verifiedHops"`
	Hops         []BGPsecHopResult `json:"hops,omitempty"`
	Reasons      []Reason          `json:"reasons,omitempty"`
}

// VerifyBGPsec verifies prefix binding, pCount, target AS and predecessor
// signatures in reverse chain order.
func VerifyBGPsec(keys *KeyStore, req BGPsecRequest) (result BGPsecResult) {
	result = BGPsecResult{Status: StatusValid}
	defer func() {
		sort.SliceStable(result.Hops, func(i, j int) bool { return result.Hops[i].Index < result.Hops[j].Index })
	}()
	prefix, err := ParsePrefix(req.Prefix)
	if err != nil {
		return bgpsecInvalid(result, "invalid_prefix", err.Error())
	}

	path := req.ASPath
	runs := req.Normalized.Runs
	if len(path) == 0 && len(req.Normalized.ASNs) > 0 {
		path = req.Normalized.ASNs
		runs = req.Normalized.Runs
	}
	if len(path) == 0 {
		normalized := NormalizeASNs(req.ASPath)
		path = normalized.ASNs
		runs = normalized.Runs
	}
	if len(path) == 0 {
		return bgpsecInvalid(result, "empty_as_path", ErrEmptyPath.Error())
	}

	proof := req.Proof
	if proof == nil {
		proof = &BGPsecProof{
			SecurePath:     req.SecurePath,
			SignatureBlock: req.SignatureBlock,
			TargetASN:      req.TargetASN,
			LocalASN:       req.LocalASN,
		}
	}
	hops := proof.hops()
	sigs := proof.sigs()
	finalTarget := firstNonZero(req.TargetASN, req.LocalASN, proof.target())

	if len(hops) != len(path) || len(sigs) != len(path) {
		return bgpsecInvalid(result, ErrCountMismatch.Error(),
			fmt.Sprintf("path=%d securePath=%d signatures=%d", len(path), len(hops), len(sigs)))
	}
	if req.RequireFinalTarget && finalTarget == 0 {
		return bgpsecInvalid(result, "missing_target_as", "final/local target AS is required")
	}

	predecessor := []byte(nil)
	for i := len(path) - 1; i >= 0; i-- {
		hop := hops[i]
		sig := sigs[i]
		hopASN := hop.HopASN()
		if hopASN == 0 {
			return bgpsecHopInvalid(&result, i, hopASN, "missing secure path ASN")
		}
		if hopASN != path[i] {
			return bgpsecHopInvalid(&result, i, hopASN,
				fmt.Sprintf("Secure_Path AS%d does not match AS_PATH AS%d", hopASN, path[i]))
		}
		if sig.signerASN() != 0 && sig.signerASN() != hopASN {
			return bgpsecHopInvalid(&result, i, hopASN, "signature signer ASN does not match Secure_Path")
		}

		pcount := hop.HopPCount()
		if pcount <= 0 || pcount > 255 {
			if !req.IgnorePCount {
				return bgpsecHopInvalid(&result, i, hopASN, fmt.Sprintf("%v: pCount=%d", ErrInvalidPCount, pcount))
			}
			result.Reasons = append(result.Reasons, Reason{
				Code:    "pcount_check_ignored",
				Message: fmt.Sprintf("weak validator ignores illegal pCount=%d", pcount),
				ASN:     hopASN, Index: i,
			})
		}
		if req.EnforceRunCounts && i < len(runs) && pcount != runs[i] {
			return bgpsecHopInvalid(&result, i, hopASN,
				fmt.Sprintf("pCount %d does not match adjacent AS run count %d", pcount, runs[i]))
		}

		target := expectedTargetForIndex(i, path, finalTarget)
		providedTarget := firstNonZero(sig.target(), hop.HopTargetASN())
		if providedTarget != 0 && target != 0 && providedTarget != target {
			return bgpsecHopInvalid(&result, i, hopASN,
				fmt.Sprintf("%v at index %d: expected AS%d got AS%d", ErrTargetAS, i, target, providedTarget))
		}
		if target == 0 && providedTarget != 0 {
			target = providedTarget
		}
		if i == 0 && req.RequireFinalTarget && target == 0 {
			return bgpsecHopInvalid(&result, i, hopASN, "missing final target AS")
		}

		sigValue := sig.value()
		if len(sigValue) == 0 {
			return bgpsecHopInvalid(&result, i, hopASN, "empty signature")
		}
		alg := sig.Algorithm
		var trusted []TrustedKey
		if keys != nil {
			trusted = keys.Lookup(hopASN, sig.keyID(), req.Now)
		}
		if len(trusted) == 0 {
			result.Hops = append(result.Hops, BGPsecHopResult{
				Index: i, SignerASN: hopASN, TargetASN: target, PCount: pcount,
				Status: StatusUnknown, Reason: ErrNoTrustedKey.Error(),
			})
			result.Status = StatusUnknown
			result.Reasons = append(result.Reasons, Reason{Code: "no_trusted_key", ASN: hopASN, Index: i})
			predecessor = append([]byte(nil), sigValue...)
			continue
		}

		payloadBuilder := req.PayloadBuilder
		if payloadBuilder == nil {
			payloadBuilder = RFC8205SigningBytes
		}
		verified := false
		var verifyErr error
		for _, key := range trusted {
			keyAlg := firstNonZeroAlg(alg, key.Algorithm)
			if keyAlg == "" {
				keyAlg = AlgorithmEd25519
			}
			payload := payloadBuilder(prefix, hop, target, predecessor, keyAlg)
			if verifySignature(keyAlg, key.PublicKey, payload, sigValue, sig.AlgorithmSuiteID) {
				verified = true
				alg = keyAlg
				break
			} else {
				verifyErr = ErrBadSignature
			}
		}
		if !verified {
			reason := ErrBadSignature.Error()
			if verifyErr != nil {
				reason = verifyErr.Error()
			}
			return bgpsecHopInvalid(&result, i, hopASN, reason)
		}

		result.VerifiedHops++
		result.Hops = append(result.Hops, BGPsecHopResult{
			Index: i, SignerASN: hopASN, TargetASN: target, PCount: pcount,
			Status: StatusValid,
		})
		predecessor = append([]byte(nil), sigValue...)
	}

	if result.Status == StatusValid && result.VerifiedHops != len(path) {
		result.Status = StatusUnknown
	}
	return result
}

func expectedTargetForIndex(i int, path []uint32, finalTarget uint32) uint32 {
	if i > 0 {
		return path[i-1]
	}
	return finalTarget
}

func firstNonZero(values ...uint32) uint32 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroAlg(values ...KeyAlgorithm) KeyAlgorithm {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func bgpsecInvalid(result BGPsecResult, code, message string) BGPsecResult {
	result.Status = StatusInvalid
	result.Reasons = append(result.Reasons, Reason{Code: code, Message: message})
	return result
}

func bgpsecHopInvalid(result *BGPsecResult, index int, asn uint32, message string) BGPsecResult {
	result.Status = StatusInvalid
	result.Hops = append(result.Hops, BGPsecHopResult{
		Index: index, SignerASN: asn, Status: StatusInvalid, Reason: message,
	})
	result.Reasons = append(result.Reasons, Reason{Code: "bgpsec_hop_invalid", Message: message, ASN: asn, Index: index})
	return *result
}

// RFC8205SigningBytes returns the deterministic signed-data representation
// used by this package. It binds algorithm, AFI/SAFI, prefix, target AS,
// pCount/flags/signer AS and the predecessor signature.
func RFC8205SigningBytes(prefix Prefix, hop SecurePathHop, targetASN uint32, predecessor []byte, alg KeyAlgorithm) []byte {
	var buffer bytes.Buffer
	suite := algorithmSuite(alg)
	writeUint16(&buffer, suite)
	afi := uint16(1)
	if prefix.AddressFamily() == 6 {
		afi = 2
	}
	writeUint16(&buffer, afi)
	buffer.WriteByte(1) // SAFI unicast
	ones := prefix.Length()
	buffer.WriteByte(byte(ones))
	prefixBytes := prefix.IP
	if prefix.AddressFamily() == 4 {
		if v4 := prefix.IP.To4(); v4 != nil {
			prefixBytes = v4
		}
	}
	byteLen := (ones + 7) / 8
	if byteLen > len(prefixBytes) {
		byteLen = len(prefixBytes)
	}
	buffer.Write(prefixBytes[:byteLen])
	writeUint32(&buffer, targetASN)
	buffer.WriteByte(byte(hop.HopPCount()))
	buffer.WriteByte(hop.Flags)
	writeUint32(&buffer, hop.HopASN())
	writeUint16(&buffer, uint16(len(predecessor)))
	buffer.Write(predecessor)
	return buffer.Bytes()
}

func algorithmSuite(alg KeyAlgorithm) uint16 {
	switch alg {
	case AlgorithmEd25519:
		return SuiteEd25519
	default:
		return SuiteECDSAP256SHA256
	}
}

func writeUint16(buffer *bytes.Buffer, value uint16) {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}

func verifySignature(alg KeyAlgorithm, publicKey any, payload, signature []byte, suiteID uint16) bool {
	if alg == "" && suiteID == SuiteEd25519 {
		alg = AlgorithmEd25519
	}
	switch alg {
	case AlgorithmEd25519:
		key, ok := publicKey.(ed25519.PublicKey)
		return ok && ed25519.Verify(key, payload, signature)
	case AlgorithmECDSAP256, AlgorithmECDSAP256SHA256, "":
		key, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return false
		}
		digest := sha256.Sum256(payload)
		if ecdsa.VerifyASN1(key, digest[:], signature) {
			return true
		}
		// Also accept fixed-width R||S signatures.
		if len(signature) == 64 {
			r := new(big.Int).SetBytes(signature[:32])
			s := new(big.Int).SetBytes(signature[32:])
			return ecdsa.Verify(key, digest[:], r, s)
		}
		return false
	default:
		return false
	}
}

// Signer is a real private BGPsec signer.
type Signer struct {
	ASN        uint32
	KeyID      []byte
	Algorithm  KeyAlgorithm
	PrivateKey any
}

// NewEd25519Signer creates an Ed25519 signer.
func NewEd25519Signer(asn uint32, privateKey ed25519.PrivateKey, keyID ...[]byte) *Signer {
	signer := &Signer{ASN: asn, Algorithm: AlgorithmEd25519, PrivateKey: privateKey}
	if len(keyID) > 0 {
		signer.KeyID = keyID[0]
	} else if len(privateKey) >= ed25519.PrivateKeySize {
		pub, ok := privateKey.Public().(ed25519.PublicKey)
		if ok {
			signer.KeyID = defaultKeyID(pub)
		}
	}
	return signer
}

// NewECDSAP256Signer creates an ECDSA-P256 signer.
func NewECDSAP256Signer(asn uint32, privateKey *ecdsa.PrivateKey, keyID ...[]byte) *Signer {
	signer := &Signer{ASN: asn, Algorithm: AlgorithmECDSAP256SHA256, PrivateKey: privateKey}
	if len(keyID) > 0 {
		signer.KeyID = keyID[0]
	} else {
		signer.KeyID = defaultKeyID(&privateKey.PublicKey)
	}
	return signer
}

// GenerateEd25519Key generates a real Ed25519 key pair.
func GenerateEd25519Key() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// GenerateECDSAP256Key generates a real ECDSA-P256 key pair.
func GenerateECDSAP256Key() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// SignPayload signs payload using the signer's real algorithm.
func (s *Signer) SignPayload(payload []byte) ([]byte, error) {
	switch s.Algorithm {
	case AlgorithmEd25519:
		key, ok := s.PrivateKey.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("pathsec: expected Ed25519 private key")
		}
		return ed25519.Sign(key, payload), nil
	case AlgorithmECDSAP256, AlgorithmECDSAP256SHA256:
		key, ok := s.PrivateKey.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("pathsec: expected ECDSA P-256 private key")
		}
		digest := sha256.Sum256(payload)
		return ecdsa.SignASN1(rand.Reader, key, digest[:])
	default:
		return nil, fmt.Errorf("pathsec: unsupported algorithm %q", s.Algorithm)
	}
}

// SignHop signs one hop and returns a Signature entry.
func (s *Signer) SignHop(prefix Prefix, hop SecurePathHop, targetASN uint32, predecessor []byte) (Signature, error) {
	if hop.HopASN() == 0 {
		hop.ASN = s.ASN
	}
	payload := RFC8205SigningBytes(prefix, hop, targetASN, predecessor, s.Algorithm)
	value, err := s.SignPayload(payload)
	if err != nil {
		return Signature{}, err
	}
	return Signature{
		SignerASN:        s.ASN,
		ASN:              s.ASN,
		KeyID:            append([]byte(nil), s.KeyID...),
		SKI:              append([]byte(nil), s.KeyID...),
		Algorithm:        s.Algorithm,
		AlgorithmSuiteID: algorithmSuite(s.Algorithm),
		Signature:        value,
		TargetASN:        targetASN,
	}, nil
}

// SignBGPsecChain builds a valid proof for a conventional nearest-to-origin
// AS_PATH. signers may be keyed by ASN; pCounts may be nil to use 1 for every
// normalized hop.
func SignBGPsecChain(prefixString string, asPath []uint32, localTargetASN uint32, signers map[uint32]*Signer, pCounts []int) (*BGPsecProof, error) {
	prefix, err := ParsePrefix(prefixString)
	if err != nil {
		return nil, err
	}
	normalized := NormalizeASNs(asPath)
	path := normalized.ASNs
	if len(path) == 0 {
		return nil, ErrEmptyPath
	}
	if pCounts == nil {
		pCounts = make([]int, len(path))
		for i := range pCounts {
			pCounts[i] = 1
		}
	}
	if len(pCounts) != len(path) {
		return nil, ErrCountMismatch
	}
	proof := &BGPsecProof{TargetASN: localTargetASN, LocalASN: localTargetASN}
	proof.SecurePath = make([]SecurePathHop, len(path))
	proof.SignatureBlock = make([]Signature, len(path))
	predecessor := []byte(nil)
	for i := len(path) - 1; i >= 0; i-- {
		signer := signers[path[i]]
		if signer == nil {
			return nil, fmt.Errorf("%w for AS%d", ErrNoTrustedKey, path[i])
		}
		hop := SecurePathHop{PCount: pCounts[i], ASN: path[i]}
		target := expectedTargetForIndex(i, path, localTargetASN)
		signature, err := signer.SignHop(prefix, hop, target, predecessor)
		if err != nil {
			return nil, err
		}
		proof.SecurePath[i] = hop
		proof.SignatureBlock[i] = signature
		predecessor = signature.Signature
	}
	return proof, nil
}

// VerifySignatureAgainstTrustedKeys exposes raw signature verification for
// host integrations that construct their own signed byte string.
func VerifySignatureAgainstTrustedKeys(keys *KeyStore, asn uint32, keyID []byte, algorithm KeyAlgorithm, payload, signature []byte) bool {
	for _, key := range keys.Lookup(asn, keyID) {
		alg := firstNonZeroAlg(algorithm, key.Algorithm)
		if verifySignature(alg, key.PublicKey, payload, signature, algorithmSuite(alg)) {
			return true
		}
	}
	return false
}

// VerifyEd25519 reports whether signature is a valid Ed25519 signature.
func VerifyEd25519(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// VerifyECDSAP256 reports whether signature is valid ASN.1 or fixed-width
// R||S ECDSA-P256 signature over the SHA-256 digest of message.
func VerifyECDSAP256(publicKey *ecdsa.PublicKey, message, signature []byte) bool {
	return verifySignature(AlgorithmECDSAP256SHA256, publicKey, message, signature, SuiteECDSAP256SHA256)
}

// SignEd25519 is a small real-signing helper for tests and control-plane hosts.
func SignEd25519(privateKey ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(privateKey, message)
}
