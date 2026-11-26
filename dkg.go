package threshold

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Round2Share is a confidential point-to-point Feldman share. It must be sent
// over an authenticated, encrypted channel; the coordinator never needs it.
type Round2Share struct {
	Dealer    ID     `json:"dealer"`
	Recipient ID     `json:"recipient"`
	Share     []byte `json:"share"`
}

// DKGPublicKey is the frozen result of a qualified DKG set.
type DKGPublicKey struct {
	Context        []byte        `json:"context"`
	Threshold      int           `json:"threshold"`
	Qualified      []ID          `json:"qualified"`
	Indices        map[ID]int    `json:"indices"`
	GroupPublicKey []byte        `json:"group_public_key"`
	PublicShares   map[ID][]byte `json:"public_shares"`
}

// KeyPackage is an honest participant's private threshold signing package.
type KeyPackage struct {
	ID                ID            `json:"id"`
	Index             int           `json:"index"`
	Threshold         int           `json:"threshold"`
	Context           []byte        `json:"context"`
	SecretShare       []byte        `json:"secret_share"`
	VerificationShare []byte        `json:"verification_share"`
	GroupPublicKey    []byte        `json:"group_public_key"`
	Qualified         []ID          `json:"qualified"`
	Indices           map[ID]int    `json:"indices"`
	PublicShares      map[ID][]byte `json:"public_shares"`
}

func (kp *KeyPackage) dkgPublicKey() *DKGPublicKey {
	return &DKGPublicKey{
		Context:        append([]byte(nil), kp.Context...),
		Threshold:      kp.Threshold,
		Qualified:      append([]ID(nil), kp.Qualified...),
		Indices:        cloneIndices(kp.Indices),
		GroupPublicKey: append([]byte(nil), kp.GroupPublicKey...),
		PublicShares:   cloneBytesMap(kp.PublicShares),
	}
}

func cloneIndices(in map[ID]int) map[ID]int {
	if in == nil {
		return nil
	}
	out := make(map[ID]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Participant is the local state machine for one DKG member.
type Participant struct {
	rc   *resolvedConfig
	self Identity

	mu           sync.Mutex
	coeffs       []*big.Int
	commitments  [][]byte
	round1       *Round1Message
	knownRound1  map[ID]*Round1Message
	issuedShares []*Round2Share
	shares       map[ID]*big.Int
}

// NewParticipant creates a DKG member and samples its Feldman polynomial.
func NewParticipant(cfg DKGConfig) (*Participant, error) {
	rc, err := resolveConfig(cfg, true)
	if err != nil {
		return nil, err
	}
	self := rc.identities[cfg.Self]
	coeffs, commitments, err := generatePolynomial(rc.group, rc.random, rc.threshold-1)
	if err != nil {
		return nil, err
	}
	return &Participant{
		rc:          rc,
		self:        self,
		coeffs:      coeffs,
		commitments: commitments,
		knownRound1: make(map[ID]*Round1Message),
		shares:      make(map[ID]*big.Int),
	}, nil
}

func cloneRound1(msg *Round1Message) *Round1Message {
	if msg == nil {
		return nil
	}
	return &Round1Message{
		Dealer:      msg.Dealer,
		Commitments: cloneByteSlices(msg.Commitments),
		PoP: SchnorrProof{
			Nonce:    append([]byte(nil), msg.PoP.Nonce...),
			Response: append([]byte(nil), msg.PoP.Response...),
		},
	}
}

// Round1 returns the participant's Feldman commitment vector and PoP. It is
// safe to call repeatedly; the same message is returned.
func (p *Participant) Round1() (*Round1Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.round1 != nil {
		return cloneRound1(p.round1), nil
	}

	c0 := p.commitments[0]
	pop, err := signPoP(p.rc.group, p.rc.random, p.rc.context, p.self.ID, p.coeffs[0], c0)
	if err != nil {
		return nil, err
	}
	p.round1 = &Round1Message{
		Dealer:      p.self.ID,
		Commitments: cloneByteSlices(p.commitments),
		PoP:         pop,
	}
	p.knownRound1[p.self.ID] = cloneRound1(p.round1)
	return cloneRound1(p.round1), nil
}

// ReceiveRound1 validates and stores another dealer's public round-one message.
func (p *Participant) ReceiveRound1(msg *Round1Message) error {
	if msg == nil {
		return fmt.Errorf("%w: nil message", ErrInvalidConfig)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	dealer, err := p.rc.identity(msg.Dealer)
	if err != nil {
		return err
	}
	if p.rc.blacklist.IsBanned(dealer.ID) {
		return fmt.Errorf("%w: %q", ErrBlacklisted, dealer.ID)
	}
	if len(msg.Commitments) != p.rc.threshold {
		return fmt.Errorf("%w: dealer=%q has %d commitments, want %d",
			ErrInvalidCommitment, dealer.ID, len(msg.Commitments), p.rc.threshold)
	}
	for i, encoded := range msg.Commitments {
		if _, err := decodePointBytes(p.rc.group, encoded); err != nil {
			return fmt.Errorf("dealer=%q C_%d: %w", dealer.ID, i, err)
		}
	}
	if err := VerifyProofOfPossession(p.rc.group, p.rc.context, dealer.ID, msg.Commitments[0], msg.PoP); err != nil {
		return err
	}
	if old, ok := p.knownRound1[dealer.ID]; ok {
		if !sameCommitments(old.Commitments, msg.Commitments) {
			return fmt.Errorf("%w: dealer=%q commitments changed", ErrDuplicateMessage, dealer.ID)
		}
		return nil
	}
	p.knownRound1[dealer.ID] = cloneRound1(msg)
	return nil
}

// CreateShares evaluates the private polynomial for every participant. The
// returned slice includes the dealer's own share.
func (p *Participant) CreateShares() ([]*Round2Share, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.round1 == nil {
		c0 := p.commitments[0]
		pop, err := signPoP(p.rc.group, p.rc.random, p.rc.context, p.self.ID, p.coeffs[0], c0)
		if err != nil {
			return nil, err
		}
		p.round1 = &Round1Message{
			Dealer:      p.self.ID,
			Commitments: cloneByteSlices(p.commitments),
			PoP:         pop,
		}
		p.knownRound1[p.self.ID] = cloneRound1(p.round1)
	}
	if p.issuedShares != nil {
		return cloneShares(p.issuedShares), nil
	}

	p.issuedShares = make([]*Round2Share, 0, len(p.rc.order))
	for _, id := range p.rc.order {
		ident := p.rc.identities[id]
		s := evaluatePolynomial(p.rc.group, p.coeffs, ident.Index)
		share := &Round2Share{
			Dealer:    p.self.ID,
			Recipient: ident.ID,
			Share:     encodeScalar(p.rc.group, s),
		}
		p.issuedShares = append(p.issuedShares, share)
		if ident.ID == p.self.ID {
			p.shares[p.self.ID] = new(big.Int).Set(s)
		}
	}
	return cloneShares(p.issuedShares), nil
}

func cloneShares(in []*Round2Share) []*Round2Share {
	out := make([]*Round2Share, len(in))
	for i, s := range in {
		out[i] = &Round2Share{Dealer: s.Dealer, Recipient: s.Recipient, Share: append([]byte(nil), s.Share...)}
	}
	return out
}

// ReceiveShare verifies a point-to-point share. If the share is invalid, the
// returned Complaint is independently verifiable and the share is not stored.
func (p *Participant) ReceiveShare(msg *Round2Share) (*Complaint, error) {
	if msg == nil {
		return nil, fmt.Errorf("%w: nil share", ErrInvalidConfig)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if msg.Recipient != p.self.ID {
		return nil, fmt.Errorf("%w: share for %q delivered to %q", ErrUnknownParticipant, msg.Recipient, p.self.ID)
	}
	dealer, err := p.rc.identity(msg.Dealer)
	if err != nil {
		return nil, err
	}
	r1, ok := p.knownRound1[dealer.ID]
	if !ok {
		return nil, fmt.Errorf("%w: dealer=%q", ErrMissingCommitment, dealer.ID)
	}

	verificationErr := VerifyShare(p.rc.group, r1.Commitments, p.self.Index, msg.Share)
	if verificationErr != nil {
		if !errors.Is(verificationErr, ErrInvalidShare) {
			return nil, verificationErr
		}
		complaint := &Complaint{
			Context:      append([]byte(nil), p.rc.context...),
			Dealer:       dealer.ID,
			Accuser:      p.self.ID,
			AccuserIndex: p.self.Index,
			Share:        append([]byte(nil), msg.Share...),
			Commitments:  cloneByteSlices(r1.Commitments),
		}
		return complaint, fmt.Errorf("%w: dealer=%q: %v", ErrInvalidShare, dealer.ID, verificationErr)
	}

	s, _ := parseScalar(p.rc.group, msg.Share)
	if old, exists := p.shares[dealer.ID]; exists {
		if old.Cmp(s) != 0 {
			return nil, fmt.Errorf("%w: dealer=%q", ErrDuplicateShare, dealer.ID)
		}
		return nil, nil
	}
	p.shares[dealer.ID] = s
	return nil, nil
}

// Finish aggregates verified shares from exactly the qualified dealer set and
// freezes this participant's local KeyPackage.
func (p *Participant) Finish(qualified []ID) (*KeyPackage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(qualified) < p.rc.threshold {
		return nil, fmt.Errorf("%w: have=%d need=%d", ErrInsufficientQualified, len(qualified), p.rc.threshold)
	}
	seen := make(map[ID]struct{}, len(qualified))
	qualifiedOrdered := make([]ID, 0, len(qualified))
	for _, id := range qualified {
		ident, err := p.rc.identity(id)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		qualifiedOrdered = append(qualifiedOrdered, id)
		_ = ident
	}
	sort.Slice(qualifiedOrdered, func(i, j int) bool {
		return p.rc.identities[qualifiedOrdered[i]].Index < p.rc.identities[qualifiedOrdered[j]].Index
	})

	secret := big.NewInt(0)
	order := p.rc.group.Order()
	commitmentVectors := make([][][]byte, 0, len(qualifiedOrdered))
	for _, dealerID := range qualifiedOrdered {
		if p.rc.blacklist.IsBanned(dealerID) {
			return nil, fmt.Errorf("%w: %q", ErrBlacklisted, dealerID)
		}
		s, ok := p.shares[dealerID]
		if !ok {
			return nil, fmt.Errorf("%w: dealer=%q", ErrMissingShare, dealerID)
		}
		r1, ok := p.knownRound1[dealerID]
		if !ok {
			return nil, fmt.Errorf("%w: dealer=%q commitment", ErrMissingCommitment, dealerID)
		}
		secret.Add(secret, s).Mod(secret, order)
		commitmentVectors = append(commitmentVectors, r1.Commitments)
	}

	c0s := make([][]byte, 0, len(commitmentVectors))
	for _, vector := range commitmentVectors {
		c0s = append(c0s, vector[0])
	}
	groupKey, err := addEncodedPoints(p.rc.group, c0s)
	if err != nil {
		return nil, err
	}

	publicShares := make(map[ID][]byte, len(qualifiedOrdered))
	indices := make(map[ID]int, len(qualifiedOrdered))
	for _, recipientID := range qualifiedOrdered {
		recipient := p.rc.identities[recipientID]
		sum := p.rc.group.ScalarBaseMult(big.NewInt(0))
		for _, vector := range commitmentVectors {
			encoded, err := evaluateCommitmentVector(p.rc.group, vector, recipient.Index)
			if err != nil {
				return nil, err
			}
			point, err := decodePointBytes(p.rc.group, encoded)
			if err != nil {
				return nil, err
			}
			sum = p.rc.group.Add(sum, point)
		}
		publicShares[recipientID] = p.rc.group.Encode(sum)
		indices[recipientID] = recipient.Index
	}

	secretBytes := encodeScalar(p.rc.group, secret)
	ownPublic := p.rc.group.Encode(p.rc.group.ScalarBaseMult(secret))
	if !bytes.Equal(ownPublic, publicShares[p.self.ID]) {
		return nil, fmt.Errorf("threshold: internal error: derived public share mismatch")
	}

	return &KeyPackage{
		ID:                p.self.ID,
		Index:             p.self.Index,
		Threshold:         p.rc.threshold,
		Context:           append([]byte(nil), p.rc.context...),
		SecretShare:       secretBytes,
		VerificationShare: ownPublic,
		GroupPublicKey:    groupKey,
		Qualified:         qualifiedOrdered,
		Indices:           indices,
		PublicShares:      cloneBytesMap(publicShares),
	}, nil
}
