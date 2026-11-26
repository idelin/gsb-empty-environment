package threshold

import (
	"fmt"
	"io"
	"math/big"
	"sort"
	"sync"
)

// SigningPackage is the round-two broadcast: the message and the canonical
// list of all signers' round-one (D,E) commitments.
type SigningPackage struct {
	Message     []byte             `json:"message"`
	Commitments []*NonceCommitment `json:"commitments"`
}

// PartialSignature is a signer's FROST round-two share z_i.
type PartialSignature struct {
	Signer  ID     `json:"signer"`
	NonceID string `json:"nonce_id"`
	Z       []byte `json:"z"`
}

// Signature is the aggregated Schnorr signature.
type Signature struct {
	R []byte `json:"r"`
	Z []byte `json:"z"`
}

// SigningState describes the one-time signing state machine.
type SigningState string

const (
	StateCollectingCommitments SigningState = "collecting_commitments"
	StateReadyForPartials      SigningState = "ready_for_partials"
	StateAggregated            SigningState = "aggregated"
	StateFailed                SigningState = "failed"
)

// PartialFailure identifies one bad partial signature.
type PartialFailure struct {
	Signer  ID     `json:"signer"`
	NonceID string `json:"nonce_id,omitempty"`
	Reason  error  `json:"-"`
}

// AccountabilityReport gives exact culprit IDs instead of a bare bool.
type AccountabilityReport struct {
	OK         bool             `json:"ok"`
	Culprits   []ID             `json:"culprits,omitempty"`
	Invalid    []PartialFailure `json:"invalid,omitempty"`
	Missing    []ID             `json:"missing,omitempty"`
	Duplicates []ID             `json:"duplicates,omitempty"`
}

// AccountabilityError wraps a report and supports errors.Is for both
// ErrAccountability and ErrInvalidPartialSignature.
type AccountabilityError struct {
	Report *AccountabilityReport
}

func (e *AccountabilityError) Error() string {
	if e == nil || e.Report == nil {
		return ErrAccountability.Error()
	}
	return fmt.Sprintf("%s: culprits=%v missing=%v duplicates=%v",
		ErrAccountability, e.Report.Culprits, e.Report.Missing, e.Report.Duplicates)
}

func (e *AccountabilityError) Unwrap() []error {
	return []error{ErrAccountability, ErrInvalidPartialSignature}
}

type preparedSigning struct {
	commitments []*NonceCommitment
	indices     map[ID]int
	binding     []byte
	rhos        map[ID]*big.Int
	lambdas     map[ID]*big.Int
	rPoint      Point
	rBytes      []byte
	challenge   *big.Int
}

func canonicalBindingList(g Group, indices map[ID]int, commitments []*NonceCommitment) ([]*NonceCommitment, []byte, error) {
	byID := make(map[ID]*NonceCommitment, len(commitments))
	for _, c := range commitments {
		if c == nil {
			return nil, nil, fmt.Errorf("%w: nil commitment", ErrInvalidNonce)
		}
		if c.Signer == "" || c.ID == "" {
			return nil, nil, fmt.Errorf("%w: empty signer or nonce ID", ErrInvalidNonce)
		}
		if _, ok := indices[c.Signer]; !ok {
			return nil, nil, fmt.Errorf("%w: signer %q", ErrUnknownParticipant, c.Signer)
		}
		if _, dup := byID[c.Signer]; dup {
			return nil, nil, fmt.Errorf("%w: signer %q in binding list", ErrDuplicateCommitment, c.Signer)
		}
		if _, err := decodePointBytes(g, c.D); err != nil {
			return nil, nil, fmt.Errorf("%w: D for %q: %v", ErrInvalidNonce, c.Signer, err)
		}
		if _, err := decodePointBytes(g, c.E); err != nil {
			return nil, nil, fmt.Errorf("%w: E for %q: %v", ErrInvalidNonce, c.Signer, err)
		}
		byID[c.Signer] = c
	}

	sorted := append([]*NonceCommitment(nil), commitments...)
	sort.Slice(sorted, func(i, j int) bool {
		ii := indices[sorted[i].Signer]
		ji := indices[sorted[j].Signer]
		if ii != ji {
			return ii < ji
		}
		return sorted[i].Signer < sorted[j].Signer
	})

	var b []byte
	for _, c := range sorted {
		indexBytes := appendUint64(nil, uint64(indices[c.Signer]))
		b = appendFrame(b, []byte(c.Signer))
		b = appendFrame(b, indexBytes)
		b = appendFrame(b, c.D)
		b = appendFrame(b, c.E)
	}
	return sorted, b, nil
}

func lagrangeCoefficient(order *big.Int, target int, all []int) *big.Int {
	x := big.NewInt(int64(target))
	numerator := big.NewInt(1)
	denominator := big.NewInt(1)
	for _, j := range all {
		if j == target {
			continue
		}
		xj := big.NewInt(int64(j))
		numerator.Mul(numerator, xj).Mod(numerator, order)
		diff := new(big.Int).Sub(xj, x)
		denominator.Mul(denominator, diff).Mod(denominator, order)
	}
	denominator.ModInverse(denominator, order)
	result := new(big.Int).Mul(numerator, denominator)
	return result.Mod(result, order)
}

func prepareSigning(g Group, indices map[ID]int, groupPublicKey, message []byte, commitments []*NonceCommitment) (*preparedSigning, error) {
	if g == nil {
		return nil, ErrGroupRequired
	}
	if len(groupPublicKey) > 0 {
		if _, err := decodePointBytes(g, groupPublicKey); err != nil {
			return nil, fmt.Errorf("%w: group public key: %v", ErrInvalidSigningPackage, err)
		}
	}
	sorted, binding, err := canonicalBindingList(g, indices, commitments)
	if err != nil {
		return nil, err
	}
	if len(sorted) == 0 {
		return nil, fmt.Errorf("%w: empty commitment list", ErrInvalidSigningPackage)
	}

	signerIndices := make([]int, 0, len(sorted))
	for _, c := range sorted {
		signerIndices = append(signerIndices, indices[c.Signer])
	}

	rhos := make(map[ID]*big.Int, len(sorted))
	lambdas := make(map[ID]*big.Int, len(sorted))
	rPoint := g.ScalarBaseMult(big.NewInt(0))
	for _, c := range sorted {
		index := indices[c.Signer]
		iBytes := appendUint64(nil, uint64(index))
		rho := g.HashToScalar(rhoDomain, []byte("H1"), iBytes, message, binding)
		rhos[c.Signer] = rho
		lambdas[c.Signer] = lagrangeCoefficient(g.Order(), index, signerIndices)

		dPoint, _ := g.Decode(c.D)
		ePoint, _ := g.Decode(c.E)
		rPoint = g.Add(rPoint, dPoint)
		rPoint = g.Add(rPoint, g.ScalarMult(ePoint, rho))
	}

	rBytes := g.Encode(rPoint)
	challenge := g.HashToScalar(challengeDomain, []byte("H2"), rBytes, groupPublicKey, message)
	return &preparedSigning{
		commitments: sorted,
		indices:     indices,
		binding:     binding,
		rhos:        rhos,
		lambdas:     lambdas,
		rPoint:      rPoint,
		rBytes:      rBytes,
		challenge:   challenge,
	}, nil
}

// BindingFactor computes rho_i = H_1(i, msg, B), where B canonically binds
// every listed signer's ID, index, D, and E commitment.
func BindingFactor(g Group, indices map[ID]int, participant ID, message []byte, commitments []*NonceCommitment) (*big.Int, error) {
	prepared, err := prepareSigning(g, indices, nil, message, commitments)
	if err != nil {
		return nil, err
	}
	rho, ok := prepared.rhos[participant]
	if !ok {
		return nil, fmt.Errorf("%w: participant %q", ErrUnknownParticipant, participant)
	}
	return new(big.Int).Set(rho), nil
}

// ComputeBindingFactor is a descriptive alias for BindingFactor.
func ComputeBindingFactor(g Group, indices map[ID]int, participant ID, message []byte, commitments []*NonceCommitment) (*big.Int, error) {
	return BindingFactor(g, indices, participant, message, commitments)
}

// Signer is an honest threshold-share signer with a one-time NonceVault.
type Signer struct {
	Group  Group
	Keys   *KeyPackage
	Nonces *NonceVault
}

func NewSigner(g Group, keys *KeyPackage, random io.Reader) (*Signer, error) {
	if g == nil {
		return nil, ErrGroupRequired
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: nil key package", ErrInvalidConfig)
	}
	vault := NewNonceVault(g, keys.ID, random)
	return &Signer{Group: g, Keys: keys, Nonces: vault}, nil
}

// Round2Sign consumes nonceID and produces a partial signature.
func (s *Signer) Round2Sign(pkg *SigningPackage, nonceID string) (*PartialSignature, error) {
	bundle, err := s.Nonces.Get(nonceID)
	if err != nil {
		return nil, err
	}
	return s.SignWithBundle(pkg, bundle)
}

// SignRoundTwo is an alias matching round-based naming.
func (s *Signer) SignRoundTwo(pkg *SigningPackage, nonceID string) (*PartialSignature, error) {
	return s.Round2Sign(pkg, nonceID)
}

// SignWithBundle performs round two while atomically consuming bundle.
func (s *Signer) SignWithBundle(pkg *SigningPackage, bundle *NonceBundle) (*PartialSignature, error) {
	if s == nil || s.Group == nil || s.Keys == nil {
		return nil, fmt.Errorf("%w: signer is not initialized", ErrInvalidConfig)
	}
	if pkg == nil {
		return nil, fmt.Errorf("%w: nil signing package", ErrInvalidSigningPackage)
	}
	if bundle == nil {
		return nil, fmt.Errorf("%w: nil nonce bundle", ErrUnknownNonce)
	}
	commitment := bundle.Public()
	if commitment.Signer != s.Keys.ID {
		return nil, fmt.Errorf("%w: nonce signer %q does not match key %q", ErrInvalidNonce, commitment.Signer, s.Keys.ID)
	}
	if nonceID := commitment.ID; nonceID == "" {
		return nil, fmt.Errorf("%w: nonce has empty ID", ErrInvalidNonce)
	}

	indices := s.Keys.Indices
	if indices == nil {
		indices = map[ID]int{s.Keys.ID: s.Keys.Index}
	}
	prepared, err := prepareSigning(s.Group, indices, s.Keys.GroupPublicKey, pkg.Message, pkg.Commitments)
	if err != nil {
		return nil, err
	}
	ownCommitment := findCommitment(pkg.Commitments, s.Keys.ID, commitment.ID)
	if ownCommitment == nil {
		return nil, fmt.Errorf("%w: own commitment missing from signing package", ErrInvalidSigningPackage)
	}

	secret, err := parseScalar(s.Group, s.Keys.SecretShare)
	if err != nil {
		return nil, err
	}
	rho := prepared.rhos[s.Keys.ID]
	lambda := prepared.lambdas[s.Keys.ID]
	if rho == nil || lambda == nil {
		return nil, fmt.Errorf("%w: own signer %q absent from binding list", ErrInvalidSigningPackage, s.Keys.ID)
	}

	partial := &PartialSignature{Signer: s.Keys.ID, NonceID: commitment.ID}
	err = bundle.Use(func(d, e *big.Int) error {
		order := s.Group.Order()
		rhoE := new(big.Int).Mul(rho, e)
		lambdaC := new(big.Int).Mul(lambda, prepared.challenge)
		lambdaCSK := lambdaC.Mul(lambdaC, secret)
		z := new(big.Int).Add(d, rhoE)
		z.Add(z, lambdaCSK).Mod(z, order)
		partial.Z = encodeScalar(s.Group, z)
		secret.SetUint64(0)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return partial, nil
}

func findCommitment(comms []*NonceCommitment, signer ID, nonceID string) *NonceCommitment {
	for _, c := range comms {
		if c != nil && c.Signer == signer && (nonceID == "" || c.ID == nonceID) {
			return c
		}
	}
	return nil
}

// VerifyPartialSignature checks one FROST partial against its public share.
func VerifyPartialSignature(g Group, keys *DKGPublicKey, pkg *SigningPackage, partial *PartialSignature) error {
	if g == nil {
		return ErrGroupRequired
	}
	if keys == nil || pkg == nil || partial == nil {
		return fmt.Errorf("%w: nil keys, package, or partial", ErrInvalidPartialSignature)
	}
	if _, ok := keys.Indices[partial.Signer]; !ok {
		return fmt.Errorf("%w: unknown signer %q", ErrInvalidPartialSignature, partial.Signer)
	}
	prepared, err := prepareSigning(g, keys.Indices, keys.GroupPublicKey, pkg.Message, pkg.Commitments)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPartialSignature, err)
	}
	commitment := findCommitment(pkg.Commitments, partial.Signer, partial.NonceID)
	if commitment == nil {
		return fmt.Errorf("%w: no matching commitment for %q", ErrInvalidPartialSignature, partial.Signer)
	}
	z, err := parseScalar(g, partial.Z)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPartialSignature, err)
	}
	yPoint, err := decodePointBytes(g, keys.PublicShares[partial.Signer])
	if err != nil {
		return fmt.Errorf("%w: public share: %v", ErrInvalidPartialSignature, err)
	}
	dPoint, err := g.Decode(commitment.D)
	if err != nil {
		return fmt.Errorf("%w: D: %v", ErrInvalidPartialSignature, err)
	}
	ePoint, err := g.Decode(commitment.E)
	if err != nil {
		return fmt.Errorf("%w: E: %v", ErrInvalidPartialSignature, err)
	}
	scalar := new(big.Int).Mul(prepared.lambdas[partial.Signer], prepared.challenge)
	left := g.ScalarBaseMult(z)
	right := g.Add(dPoint, g.ScalarMult(ePoint, prepared.rhos[partial.Signer]))
	right = g.Add(right, g.ScalarMult(yPoint, scalar))
	if !g.Equal(left, right) {
		return fmt.Errorf("%w: signer=%q", ErrInvalidPartialSignature, partial.Signer)
	}
	return nil
}

// SigningSession is the coordinator/aggregator two-round state machine.
type SigningSession struct {
	group   Group
	keys    *DKGPublicKey
	message []byte

	mu           sync.Mutex
	state        SigningState
	comms        map[ID]*NonceCommitment
	fingerprints map[string]ID
	nonceIDs     map[string]ID
	pkg          *SigningPackage
	signature    *Signature
}

// NewSigningSession creates a coordinator for one message and frozen DKG key.
func NewSigningSession(g Group, keys *DKGPublicKey, message []byte) (*SigningSession, error) {
	if g == nil {
		return nil, ErrGroupRequired
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: nil DKG public key", ErrInvalidConfig)
	}
	if keys.Threshold < 1 || len(keys.Qualified) < keys.Threshold {
		return nil, fmt.Errorf("%w: qualified=%d threshold=%d", ErrInvalidThreshold, len(keys.Qualified), keys.Threshold)
	}
	if len(keys.Indices) != len(keys.PublicShares) {
		return nil, fmt.Errorf("%w: indices/public shares mismatch", ErrInvalidConfig)
	}
	if _, err := decodePointBytes(g, keys.GroupPublicKey); err != nil {
		return nil, err
	}
	return &SigningSession{
		group:        g,
		keys:         cloneDKGPublicKey(keys),
		message:      append([]byte(nil), message...),
		state:        StateCollectingCommitments,
		comms:        make(map[ID]*NonceCommitment),
		fingerprints: make(map[string]ID),
		nonceIDs:     make(map[string]ID),
	}, nil
}

// NewSigningCoordinator is a terminology alias for NewSigningSession.
func NewSigningCoordinator(g Group, keys *DKGPublicKey, message []byte) (*SigningSession, error) {
	return NewSigningSession(g, keys, message)
}

func (s *SigningSession) State() SigningState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// AddCommitment accepts one signer's public (D,E) commitment exactly once.
func (s *SigningSession) AddCommitment(c *NonceCommitment) error {
	if c == nil {
		return fmt.Errorf("%w: nil commitment", ErrInvalidNonce)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateCollectingCommitments {
		return fmt.Errorf("%w: cannot add commitment in state %q", ErrRoundViolation, s.state)
	}
	if _, ok := s.keys.Indices[c.Signer]; !ok {
		return fmt.Errorf("%w: signer %q is not qualified", ErrUnknownParticipant, c.Signer)
	}
	if _, ok := s.keys.PublicShares[c.Signer]; !ok {
		return fmt.Errorf("%w: missing public share for %q", ErrUnknownParticipant, c.Signer)
	}
	if _, err := decodePointBytes(s.group, c.D); err != nil {
		return fmt.Errorf("%w: D: %v", ErrInvalidNonce, err)
	}
	if _, err := decodePointBytes(s.group, c.E); err != nil {
		return fmt.Errorf("%w: E: %v", ErrInvalidNonce, err)
	}
	if old, ok := s.comms[c.Signer]; ok {
		if old.ID == c.ID && bytesEqual(old.D, c.D) && bytesEqual(old.E, c.E) {
			return nil
		}
		return fmt.Errorf("%w: signer=%q", ErrDuplicateCommitment, c.Signer)
	}
	fingerprint := string(c.D) + "\x00" + string(c.E)
	if prior, ok := s.fingerprints[fingerprint]; ok {
		return fmt.Errorf("%w: (D,E) already registered by %q and replayed by %q", ErrNonceReuse, prior, c.Signer)
	}
	if prior, ok := s.nonceIDs[c.ID]; ok && prior != c.Signer {
		return fmt.Errorf("%w: nonce ID %q already registered by %q", ErrNonceReuse, c.ID, prior)
	}
	s.comms[c.Signer] = &NonceCommitment{
		ID:     c.ID,
		Signer: c.Signer,
		D:      append([]byte(nil), c.D...),
		E:      append([]byte(nil), c.E...),
	}
	s.fingerprints[fingerprint] = c.Signer
	s.nonceIDs[c.ID] = c.Signer
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FinalizeCommitments freezes the canonical commitment list and releases the
// signing package needed by round two.
func (s *SigningSession) FinalizeCommitments() (*SigningPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pkg != nil {
		return cloneSigningPackage(s.pkg), nil
	}
	if len(s.comms) < s.keys.Threshold {
		return nil, fmt.Errorf("%w: commitments=%d threshold=%d",
			ErrInsufficientQualified, len(s.comms), s.keys.Threshold)
	}
	list := make([]*NonceCommitment, 0, len(s.comms))
	for _, c := range s.comms {
		list = append(list, c)
	}
	if _, _, err := canonicalBindingList(s.group, s.keys.Indices, list); err != nil {
		return nil, err
	}
	s.pkg = &SigningPackage{Message: append([]byte(nil), s.message...), Commitments: list}
	s.state = StateReadyForPartials
	return cloneSigningPackage(s.pkg), nil
}

func cloneSigningPackage(in *SigningPackage) *SigningPackage {
	if in == nil {
		return nil
	}
	out := &SigningPackage{Message: append([]byte(nil), in.Message...)}
	for _, c := range in.Commitments {
		out.Commitments = append(out.Commitments, &NonceCommitment{
			ID: c.ID, Signer: c.Signer,
			D: append([]byte(nil), c.D...), E: append([]byte(nil), c.E...),
		})
	}
	return out
}

// SigningPackage returns the finalized package after FinalizeCommitments.
func (s *SigningSession) SigningPackage() (*SigningPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pkg == nil {
		return nil, fmt.Errorf("%w: commitments not finalized", ErrRoundViolation)
	}
	return cloneSigningPackage(s.pkg), nil
}

// VerifyPartials verifies every partial against its individual public share.
func (s *SigningSession) VerifyPartials(partials []*PartialSignature) (*AccountabilityReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pkg == nil {
		return nil, fmt.Errorf("%w: commitments not finalized", ErrRoundViolation)
	}
	report := s.verifyPartialsLocked(partials)
	return report, nil
}

func (s *SigningSession) verifyPartialsLocked(partials []*PartialSignature) *AccountabilityReport {
	report := &AccountabilityReport{OK: true}
	prepared, err := prepareSigning(s.group, s.keys.Indices, s.keys.GroupPublicKey, s.message, s.pkg.Commitments)
	if err != nil {
		report.OK = false
		report.Invalid = append(report.Invalid, PartialFailure{Reason: err})
		report.Culprits = append(report.Culprits, "")
		return report
	}

	seen := make(map[ID]int)
	verifiedZ := make(map[ID]*big.Int)
	for _, partial := range partials {
		if partial == nil {
			report.OK = false
			report.Invalid = append(report.Invalid, PartialFailure{Reason: fmt.Errorf("%w: nil partial", ErrInvalidPartialSignature)})
			continue
		}
		if _, known := s.keys.Indices[partial.Signer]; !known {
			report.OK = false
			report.Invalid = append(report.Invalid, PartialFailure{
				Signer: partial.Signer, NonceID: partial.NonceID,
				Reason: fmt.Errorf("%w: unknown signer", ErrUnknownParticipant),
			})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			continue
		}
		seen[partial.Signer]++
		if seen[partial.Signer] > 1 {
			report.Duplicates = appendUniqueID(report.Duplicates, partial.Signer)
			report.OK = false
		}

		commitment := findCommitment(s.pkg.Commitments, partial.Signer, partial.NonceID)
		if commitment == nil {
			reason := fmt.Errorf("%w: no matching commitment/nonce ID", ErrInvalidPartialSignature)
			report.Invalid = append(report.Invalid, PartialFailure{Signer: partial.Signer, NonceID: partial.NonceID, Reason: reason})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			report.OK = false
			continue
		}
		if partial.NonceID != commitment.ID {
			reason := fmt.Errorf("%w: nonce ID mismatch", ErrInvalidPartialSignature)
			report.Invalid = append(report.Invalid, PartialFailure{Signer: partial.Signer, NonceID: partial.NonceID, Reason: reason})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			report.OK = false
			continue
		}
		z, err := parseScalar(s.group, partial.Z)
		if err != nil {
			wrapped := fmt.Errorf("%w: signer=%q: %v", ErrInvalidPartialSignature, partial.Signer, err)
			report.Invalid = append(report.Invalid, PartialFailure{Signer: partial.Signer, NonceID: partial.NonceID, Reason: wrapped})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			report.OK = false
			continue
		}

		yBytes := s.keys.PublicShares[partial.Signer]
		yPoint, err := decodePointBytes(s.group, yBytes)
		if err != nil {
			wrapped := fmt.Errorf("%w: public share: %v", ErrInvalidPartialSignature, err)
			report.Invalid = append(report.Invalid, PartialFailure{Signer: partial.Signer, Reason: wrapped})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			report.OK = false
			continue
		}

		rho := prepared.rhos[partial.Signer]
		lambda := prepared.lambdas[partial.Signer]
		dPoint, _ := s.group.Decode(commitment.D)
		ePoint, _ := s.group.Decode(commitment.E)

		accountabilityScalar := new(big.Int).Mul(lambda, prepared.challenge)
		left := s.group.ScalarBaseMult(z)
		right := s.group.Add(dPoint, s.group.Add(
			s.group.ScalarMult(ePoint, rho),
			s.group.ScalarMult(yPoint, accountabilityScalar),
		))

		if !s.group.Equal(left, right) {
			reason := fmt.Errorf("%w: signer=%q failed Schnorr equation", ErrInvalidPartialSignature, partial.Signer)
			report.Invalid = append(report.Invalid, PartialFailure{Signer: partial.Signer, NonceID: partial.NonceID, Reason: reason})
			report.Culprits = appendUniqueID(report.Culprits, partial.Signer)
			report.OK = false
			continue
		}
		if seen[partial.Signer] == 1 {
			verifiedZ[partial.Signer] = z
		}
	}

	for _, c := range s.pkg.Commitments {
		if seen[c.Signer] == 0 {
			report.Missing = append(report.Missing, c.Signer)
			report.OK = false
		}
	}
	if len(report.Duplicates) > 0 {
		report.OK = false
	}
	sortIDs(s.keys.Indices, report.Culprits)
	sortIDs(s.keys.Indices, report.Missing)
	sortIDs(s.keys.Indices, report.Duplicates)
	return report
}

func appendUniqueID(ids []ID, id ID) []ID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func sortIDs(indices map[ID]int, ids []ID) {
	sort.Slice(ids, func(i, j int) bool {
		ii, oi := indices[ids[i]]
		ji, oj := indices[ids[j]]
		if oi && oj && ii != ji {
			return ii < ji
		}
		return ids[i] < ids[j]
	})
}

// Aggregate verifies all partials, identifies bad signers, and only then
// aggregates z values. On failure the returned error is *AccountabilityError.
func (s *SigningSession) Aggregate(partials []*PartialSignature) (*Signature, *AccountabilityReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signature != nil && s.state == StateAggregated {
		report := &AccountabilityReport{OK: true}
		return cloneSignature(s.signature), report, nil
	}
	if s.pkg == nil {
		return nil, nil, fmt.Errorf("%w: commitments not finalized", ErrRoundViolation)
	}

	report := s.verifyPartialsLocked(partials)
	if !report.OK {
		s.state = StateFailed
		return nil, report, &AccountabilityError{Report: report}
	}

	prepared, err := prepareSigning(s.group, s.keys.Indices, s.keys.GroupPublicKey, s.message, s.pkg.Commitments)
	if err != nil {
		s.state = StateFailed
		return nil, report, err
	}
	order := s.group.Order()
	zSum := big.NewInt(0)
	for _, partial := range partials {
		if partial == nil {
			continue
		}
		z, err := parseScalar(s.group, partial.Z)
		if err != nil {
			s.state = StateFailed
			return nil, report, err
		}
		zSum.Add(zSum, z).Mod(zSum, order)
	}
	signature := &Signature{R: append([]byte(nil), prepared.rBytes...), Z: encodeScalar(s.group, zSum)}
	if err := VerifySignature(s.group, s.keys.GroupPublicKey, s.message, signature); err != nil {
		s.state = StateFailed
		return nil, report, err
	}
	s.signature = signature
	s.state = StateAggregated
	return cloneSignature(signature), &AccountabilityReport{OK: true}, nil
}

func cloneSignature(in *Signature) *Signature {
	if in == nil {
		return nil
	}
	return &Signature{R: append([]byte(nil), in.R...), Z: append([]byte(nil), in.Z...)}
}

// VerifySignature verifies an aggregated Schnorr signature: zG == R+cY.
func VerifySignature(g Group, groupPublicKey, message []byte, signature *Signature) error {
	if g == nil {
		return ErrGroupRequired
	}
	if signature == nil {
		return fmt.Errorf("%w: nil signature", ErrInvalidSignature)
	}
	pk, err := decodePointBytes(g, groupPublicKey)
	if err != nil {
		return fmt.Errorf("%w: public key: %v", ErrInvalidSignature, err)
	}
	rPoint, err := decodePointBytes(g, signature.R)
	if err != nil {
		return fmt.Errorf("%w: R: %v", ErrInvalidSignature, err)
	}
	z, err := parseScalar(g, signature.Z)
	if err != nil {
		return fmt.Errorf("%w: z: %v", ErrInvalidSignature, err)
	}
	challenge := g.HashToScalar(challengeDomain, []byte("H2"), signature.R, groupPublicKey, message)
	left := g.ScalarBaseMult(z)
	right := g.Add(rPoint, g.ScalarMult(pk, challenge))
	if !g.Equal(left, right) {
		return ErrInvalidSignature
	}
	return nil
}

// Verify is a short alias for VerifySignature.
func Verify(g Group, groupPublicKey, message []byte, signature *Signature) error {
	return VerifySignature(g, groupPublicKey, message, signature)
}
