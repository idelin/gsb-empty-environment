package threshold

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Coordinator performs public DKG validation and accountability. It never
// sees private point-to-point shares unless a share is submitted as evidence
// in a complaint.
type Coordinator struct {
	rc *resolvedConfig

	mu         sync.Mutex
	round1     map[ID]*Round1Message
	complaints []Complaint
	frozen     *DKGPublicKey
}

func NewCoordinator(cfg DKGConfig) (*Coordinator, error) {
	rc, err := resolveConfig(cfg, false)
	if err != nil {
		return nil, err
	}
	return &Coordinator{rc: rc, round1: make(map[ID]*Round1Message)}, nil
}

func (c *Coordinator) Group() Group          { return c.rc.group }
func (c *Coordinator) Blacklist() *Blacklist { return c.rc.blacklist }
func (c *Coordinator) Threshold() int        { return c.rc.threshold }

// HandleRound1 validates the exact commitment degree and PoP. A known sender
// with an invalid message is permanently blacklisted.
func (c *Coordinator) HandleRound1(msg *Round1Message) error {
	if msg == nil {
		return fmt.Errorf("%w: nil round-one message", ErrInvalidConfig)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		return fmt.Errorf("%w: round one after freeze", ErrFrozen)
	}

	dealer, err := c.rc.identity(msg.Dealer)
	if err != nil {
		return err
	}
	if c.rc.blacklist.IsBanned(dealer.ID) {
		return fmt.Errorf("%w: %q", ErrBlacklisted, dealer.ID)
	}

	if len(msg.Commitments) != c.rc.threshold {
		reason := fmt.Sprintf("commitment degree/count mismatch: got %d want %d", len(msg.Commitments), c.rc.threshold)
		c.banLocked(dealer.ID, BanInvalidRound1, reason)
		return fmt.Errorf("%w: dealer=%q: %s", ErrInvalidCommitment, dealer.ID, reason)
	}
	for i, encoded := range msg.Commitments {
		if _, err := decodePointBytes(c.rc.group, encoded); err != nil {
			reason := fmt.Sprintf("C_%d invalid: %v", i, err)
			c.banLocked(dealer.ID, BanInvalidRound1, reason)
			return fmt.Errorf("dealer=%q C_%d: %w", dealer.ID, i, err)
		}
	}
	if err := VerifyProofOfPossession(c.rc.group, c.rc.context, dealer.ID, msg.Commitments[0], msg.PoP); err != nil {
		c.banLocked(dealer.ID, BanInvalidRound1, err.Error())
		return err
	}

	if old, ok := c.round1[dealer.ID]; ok {
		if !sameCommitments(old.Commitments, msg.Commitments) {
			return fmt.Errorf("%w: dealer=%q published conflicting commitments", ErrDuplicateMessage, dealer.ID)
		}
		return nil
	}
	c.round1[dealer.ID] = cloneRound1(msg)
	return nil
}

func (c *Coordinator) banLocked(id ID, reason BanReason, detail string) BanRecord {
	return c.rc.blacklist.Ban(id, reason, detail)
}

// SubmitComplaint independently verifies evidence and, if true, permanently
// bans and excludes the accused dealer.
func (c *Coordinator) SubmitComplaint(complaint *Complaint) (BanRecord, error) {
	if complaint == nil {
		return BanRecord{}, fmt.Errorf("%w: nil complaint", ErrInvalidComplaint)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		return BanRecord{}, fmt.Errorf("%w: complaint after freeze", ErrFrozen)
	}

	dealer, err := c.rc.identity(complaint.Dealer)
	if err != nil {
		return BanRecord{}, err
	}
	if _, err := c.rc.identity(complaint.Accuser); err != nil {
		return BanRecord{}, err
	}
	if record, banned := c.rc.blacklist.Record(dealer.ID); banned {
		return record, nil
	}

	stored, hasRound1 := c.round1[dealer.ID]
	if !hasRound1 {
		return BanRecord{}, fmt.Errorf("%w: dealer=%q has no canonical round one", ErrMissingCommitment, dealer.ID)
	}
	if len(complaint.Commitments) == 0 {
		complaint.Commitments = cloneByteSlices(stored.Commitments)
	} else if !sameCommitments(complaint.Commitments, stored.Commitments) {
		return BanRecord{}, fmt.Errorf("%w: dealer=%q commitments are not canonical", ErrInvalidComplaint, dealer.ID)
	}
	if len(complaint.Context) == 0 {
		complaint.Context = append([]byte(nil), c.rc.context...)
	} else if !bytes.Equal(complaint.Context, c.rc.context) {
		return BanRecord{}, fmt.Errorf("%w: complaint context mismatch", ErrInvalidComplaint)
	}

	if err := VerifyComplaint(c.rc.group, *complaint); err != nil {
		return BanRecord{}, fmt.Errorf("%w: %v", ErrInvalidComplaint, err)
	}

	record := c.banLocked(dealer.ID, BanBadShare, "independently verified Feldman share mismatch")
	c.complaints = append(c.complaints, *cloneComplaint(complaint))
	return record, nil
}

// HandleComplaint is an error-only alias for SubmitComplaint.
func (c *Coordinator) HandleComplaint(complaint *Complaint) error {
	_, err := c.SubmitComplaint(complaint)
	return err
}

func cloneComplaint(in *Complaint) *Complaint {
	if in == nil {
		return nil
	}
	return &Complaint{
		Context:      append([]byte(nil), in.Context...),
		Dealer:       in.Dealer,
		Accuser:      in.Accuser,
		AccuserIndex: in.AccuserIndex,
		Share:        append([]byte(nil), in.Share...),
		Commitments:  cloneByteSlices(in.Commitments),
	}
}

// Complaints returns the verified complaints accepted by the coordinator.
func (c *Coordinator) Complaints() []Complaint {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Complaint, len(c.complaints))
	for i := range c.complaints {
		out[i] = *cloneComplaint(&c.complaints[i])
	}
	return out
}

// Freeze computes the joint key and public shares for all qualified parties.
// It succeeds only when at least t valid, non-blacklisted dealers remain.
func (c *Coordinator) Freeze() (*DKGPublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen != nil {
		return cloneDKGPublicKey(c.frozen), nil
	}

	qualified := make([]ID, 0, len(c.rc.order))
	vectors := make(map[ID][][]byte)
	for _, id := range c.rc.order {
		if c.rc.blacklist.IsBanned(id) {
			continue
		}
		msg, ok := c.round1[id]
		if !ok {
			continue
		}
		qualified = append(qualified, id)
		vectors[id] = msg.Commitments
	}
	if len(qualified) < c.rc.threshold {
		return nil, fmt.Errorf("%w: qualified=%d threshold=%d blacklisted=%v",
			ErrInsufficientQualified, len(qualified), c.rc.threshold, c.rc.blacklist.IDs())
	}

	pk, err := buildDKGPublicKey(c.rc, qualified, vectors)
	if err != nil {
		return nil, err
	}
	c.frozen = pk
	return cloneDKGPublicKey(pk), nil
}

func buildDKGPublicKey(rc *resolvedConfig, qualified []ID, vectors map[ID][][]byte) (*DKGPublicKey, error) {
	ordered := append([]ID(nil), qualified...)
	sort.Slice(ordered, func(i, j int) bool {
		return rc.identities[ordered[i]].Index < rc.identities[ordered[j]].Index
	})

	c0s := make([][]byte, 0, len(ordered))
	for _, id := range ordered {
		vector := vectors[id]
		if len(vector) != rc.threshold {
			return nil, fmt.Errorf("%w: dealer=%q vector length", ErrInvalidCommitment, id)
		}
		c0s = append(c0s, vector[0])
	}
	groupKey, err := addEncodedPoints(rc.group, c0s)
	if err != nil {
		return nil, err
	}

	indices := make(map[ID]int, len(ordered))
	publicShares := make(map[ID][]byte, len(ordered))
	for _, recipientID := range ordered {
		recipient := rc.identities[recipientID]
		sum := rc.group.ScalarBaseMult(bigZero())
		for _, dealerID := range ordered {
			encoded, err := evaluateCommitmentVector(rc.group, vectors[dealerID], recipient.Index)
			if err != nil {
				return nil, err
			}
			point, err := decodePointBytes(rc.group, encoded)
			if err != nil {
				return nil, err
			}
			sum = rc.group.Add(sum, point)
		}
		publicShares[recipientID] = rc.group.Encode(sum)
		indices[recipientID] = recipient.Index
	}

	return &DKGPublicKey{
		Context:        append([]byte(nil), rc.context...),
		Threshold:      rc.threshold,
		Qualified:      ordered,
		Indices:        indices,
		GroupPublicKey: groupKey,
		PublicShares:   cloneBytesMap(publicShares),
	}, nil
}

func cloneDKGPublicKey(in *DKGPublicKey) *DKGPublicKey {
	if in == nil {
		return nil
	}
	return &DKGPublicKey{
		Context:        append([]byte(nil), in.Context...),
		Threshold:      in.Threshold,
		Qualified:      append([]ID(nil), in.Qualified...),
		Indices:        cloneIndices(in.Indices),
		GroupPublicKey: append([]byte(nil), in.GroupPublicKey...),
		PublicShares:   cloneBytesMap(in.PublicShares),
	}
}

func bigZero() *big.Int { return new(big.Int).SetInt64(0) }
