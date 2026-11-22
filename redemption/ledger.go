package redemption

import (
	"fmt"
	"sort"
)

// SignatureVerifier verifies an issuer signature on an origin note. Hosts
// inject their own implementation; nil disables the check (the host then
// guarantees note authenticity through another channel).
type SignatureVerifier func(note OriginNote) error

// ProofVerifier verifies the anonymous redemption proof binding a
// transaction's input commitments to its entitlements and change
// commitments. Hosts inject their own implementation; nil disables the check.
type ProofVerifier func(tx Transaction) error

type noteData struct {
	issuer     IssuerID
	activity   ActivityID
	commitment Commitment
	amount     int64
}

type candidateInfo struct {
	tx           Transaction
	index        int
	entitlements int64
	preds        []int // canonical candidate indices creating spent change outpoints
	predMask     uint32
	predTxIDs    []TxID
	blocked      bool // conflicts irrecoverably with the confirmed prefix
	blockReason  error
}

// Ledger is a validated, indexed view of a Problem.
type Ledger struct {
	problem    Problem
	issuers    map[IssuerID]bool
	quotas     map[BalanceKey]int64
	candidates []candidateInfo
	byID       map[TxID]int

	// notes created by transactions (confirmed and candidates).
	notes map[Outpoint]noteData
	// nullifiers and outpoints already spent by the confirmed prefix.
	prefixNullifiers map[Nullifier]bool
	prefixOutpoints  map[Outpoint]bool

	// per-candidate derived bookkeeping, keyed by canonical index.
	nullifierUse map[int][]Nullifier
	outpointUse  map[int][]Outpoint
	spendByKey   map[int]map[BalanceKey]int64
}

// Prepare validates the problem structurally, runs the host-injected
// signature and proof verifiers, and builds the indexed ledger used by the
// solver and the certificate checker.
func Prepare(p Problem, sigVerifier SignatureVerifier, proofVerifier ProofVerifier) (*Ledger, error) {
	l := &Ledger{
		issuers:          make(map[IssuerID]bool),
		quotas:           make(map[BalanceKey]int64),
		byID:             make(map[TxID]int),
		notes:            make(map[Outpoint]noteData),
		prefixNullifiers: make(map[Nullifier]bool),
		prefixOutpoints:  make(map[Outpoint]bool),
		nullifierUse:     make(map[int][]Nullifier),
		outpointUse:      make(map[int][]Outpoint),
		spendByKey:       make(map[int]map[BalanceKey]int64),
	}
	for _, id := range p.Issuers {
		if id == "" {
			return nil, fmt.Errorf("%w: empty issuer id", ErrMalformed)
		}
		l.issuers[id] = true
	}
	for _, q := range p.Quotas {
		if q.Limit < 0 {
			return nil, fmt.Errorf("%w: negative quota for %s/%s", ErrMalformed, q.Issuer, q.Activity)
		}
		key := BalanceKey{Issuer: q.Issuer, Activity: q.Activity}
		if _, dup := l.quotas[key]; dup {
			return nil, fmt.Errorf("%w: duplicate quota for %s/%s", ErrMalformed, q.Issuer, q.Activity)
		}
		l.quotas[key] = q.Limit
	}

	seenID := make(map[TxID]bool)
	checkID := func(id TxID) error {
		if id == "" {
			return fmt.Errorf("%w: empty transaction id", ErrMalformed)
		}
		if seenID[id] {
			return fmt.Errorf("%w: duplicate transaction id %q", ErrMalformed, id)
		}
		seenID[id] = true
		return nil
	}

	// Index every output first so cross-references resolve regardless of
	// ordering within the imported document.
	for i := range p.Confirmed {
		if err := checkID(p.Confirmed[i].ID); err != nil {
			return nil, err
		}
		l.indexOutputs(&p.Confirmed[i])
	}
	candIDs := make(map[TxID]bool)
	for i := range p.Candidates {
		if err := checkID(p.Candidates[i].ID); err != nil {
			return nil, err
		}
		candIDs[p.Candidates[i].ID] = true
		l.indexOutputs(&p.Candidates[i])
	}
	confirmedIDs := make(map[TxID]bool)
	for i := range p.Confirmed {
		confirmedIDs[p.Confirmed[i].ID] = true
	}

	// Validate the confirmed prefix in order, simulating its spend set.
	for i := range p.Confirmed {
		if err := l.validateTransaction(&p.Confirmed[i], sigVerifier, proofVerifier); err != nil {
			return nil, err
		}
		if err := l.recordConfirmed(&p.Confirmed[i]); err != nil {
			return nil, err
		}
	}

	// Validate candidates and derive dependencies.
	l.candidates = make([]candidateInfo, len(p.Candidates))
	for i := range p.Candidates {
		tx := &p.Candidates[i]
		if err := l.validateTransaction(tx, sigVerifier, proofVerifier); err != nil {
			return nil, err
		}
		info := candidateInfo{tx: *tx, entitlements: tx.entitlementUnits()}
		info.predTxIDs, info.blocked, info.blockReason = l.deriveDeps(tx)
		l.candidates[i] = info
	}

	// Canonical decision order: ascending transaction ID. This makes the
	// bitmask one-wins rule identical to the lexicographic transaction-ID
	// sequence tie-break, keeping tie-aware pruning provably exact. Closure
	// is enforced per include-decision and acyclicity by Feasible.
	sort.SliceStable(l.candidates, func(a, b int) bool {
		return l.candidates[a].tx.ID < l.candidates[b].tx.ID
	})
	for i := range l.candidates {
		l.candidates[i].index = i
		l.byID[l.candidates[i].tx.ID] = i
	}
	for i := range l.candidates {
		for _, predID := range l.candidates[i].predTxIDs {
			if predIdx, ok := l.byID[predID]; ok {
				l.candidates[i].preds = append(l.candidates[i].preds, predIdx)
				l.candidates[i].predMask |= 1 << uint(predIdx)
			} else if !confirmedIDs[predID] {
				return nil, fmt.Errorf("%w: transaction %q references unknown predecessor %q",
					ErrUnknownNote, l.candidates[i].tx.ID, predID)
			}
			// Confirmed predecessors are always available and need no
			// candidate dependency edge.
		}
		l.recordCandidateBookkeeping(i, &l.candidates[i].tx)
	}
	if len(l.candidates) > 22 {
		return nil, fmt.Errorf("%w: more than 22 candidates (%d)", ErrMalformed, len(l.candidates))
	}
	l.problem = p
	return l, nil
}

func (l *Ledger) indexOutputs(tx *Transaction) {
	for j, out := range tx.Outputs {
		l.notes[Outpoint{TxID: tx.ID, OutIndex: uint8(j)}] = noteData{
			issuer:     out.Issuer,
			activity:   out.Activity,
			commitment: out.Commitment,
			amount:     out.Amount,
		}
	}
}

func (tx *Transaction) entitlementUnits() int64 {
	var total int64
	for _, s := range tx.Spends {
		total += s.Units
	}
	return total
}

func (l *Ledger) validateTransaction(tx *Transaction, sigVerifier SignatureVerifier, proofVerifier ProofVerifier) error {
	if len(tx.Inputs) == 0 {
		return fmt.Errorf("%w: transaction %q has no inputs", ErrMalformed, tx.ID)
	}
	seenNull := make(map[Nullifier]bool)
	seenOut := make(map[Outpoint]bool)
	inSum := make(map[BalanceKey]int64)
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		if in.Amount <= 0 {
			return fmt.Errorf("%w: transaction %q input %d has non-positive amount", ErrMalformed, tx.ID, i)
		}
		if in.Nullifier == "" || in.Commitment == "" || in.Issuer == "" || in.Activity == "" {
			return fmt.Errorf("%w: transaction %q input %d missing revealed fields", ErrMalformed, tx.ID, i)
		}
		if seenNull[in.Nullifier] {
			return fmt.Errorf("%w: transaction %q input %d repeats nullifier %q", ErrMalformed, tx.ID, i, in.Nullifier)
		}
		seenNull[in.Nullifier] = true
		key := BalanceKey{Issuer: in.Issuer, Activity: in.Activity}
		inSum[key] += in.Amount

		if (in.Source.Origin == nil) == (in.Source.Outpoint == nil) {
			return fmt.Errorf("%w: transaction %q input %d must have exactly one source", ErrMalformed, tx.ID, i)
		}
		if in.Source.Origin != nil {
			o := in.Source.Origin
			if o.Issuer != in.Issuer || o.Activity != in.Activity || o.Commitment != in.Commitment ||
				o.Denomination != in.Amount || o.Nullifier != in.Nullifier {
				return fmt.Errorf("%w: transaction %q input %d origin fields disagree", ErrNoteMismatch, tx.ID, i)
			}
			if sigVerifier != nil {
				if err := sigVerifier(*o); err != nil {
					return fmt.Errorf("%w: transaction %q: %v", ErrSignature, tx.ID, err)
				}
			}
		} else {
			op := *in.Source.Outpoint
			if seenOut[op] {
				return fmt.Errorf("%w: transaction %q spends outpoint %v twice", ErrDoubleSpend, tx.ID, op)
			}
			seenOut[op] = true
			note, ok := l.notes[op]
			if !ok {
				return fmt.Errorf("%w: transaction %q input %d references unknown outpoint %+v", ErrUnknownNote, tx.ID, i, op)
			}
			if note.issuer != in.Issuer || note.activity != in.Activity ||
				note.commitment != in.Commitment || note.amount != in.Amount {
				return fmt.Errorf("%w: transaction %q input %d does not match note at %+v", ErrNoteMismatch, tx.ID, i, op)
			}
		}
	}

	outSum := make(map[BalanceKey]int64)
	for j, out := range tx.Outputs {
		if out.Amount < 0 || out.Commitment == "" || out.Issuer == "" || out.Activity == "" {
			return fmt.Errorf("%w: transaction %q output %d malformed", ErrMalformed, tx.ID, j)
		}
		key := BalanceKey{Issuer: out.Issuer, Activity: out.Activity}
		outSum[key] += out.Amount
	}
	costSum := make(map[BalanceKey]int64)
	for j, s := range tx.Spends {
		if s.Units <= 0 || s.Cost < 0 || s.Service == "" || s.Issuer == "" || s.Activity == "" {
			return fmt.Errorf("%w: transaction %q spend %d malformed", ErrMalformed, tx.ID, j)
		}
		key := BalanceKey{Issuer: s.Issuer, Activity: s.Activity}
		costSum[key] += s.Cost
	}

	// Per-activity, per-issuer conservation: inputs == change + service cost.
	keys := make(map[BalanceKey]bool)
	for k := range inSum {
		keys[k] = true
	}
	for k := range outSum {
		keys[k] = true
	}
	for k := range costSum {
		keys[k] = true
	}
	for k := range keys {
		if inSum[k] != outSum[k]+costSum[k] {
			return fmt.Errorf("%w: transaction %q key %s/%s: in=%d change=%d cost=%d",
				ErrConservation, tx.ID, k.Issuer, k.Activity, inSum[k], outSum[k], costSum[k])
		}
	}

	if proofVerifier != nil {
		if err := proofVerifier(*tx); err != nil {
			return fmt.Errorf("%w: transaction %q: %v", ErrProof, tx.ID, err)
		}
	}
	return nil
}

func (l *Ledger) recordConfirmed(tx *Transaction) error {
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		if l.prefixNullifiers[in.Nullifier] {
			return fmt.Errorf("%w: confirmed prefix repeats nullifier %q", ErrDoubleSpend, in.Nullifier)
		}
		l.prefixNullifiers[in.Nullifier] = true
		if in.Source.Outpoint != nil {
			op := *in.Source.Outpoint
			if l.prefixOutpoints[op] {
				return fmt.Errorf("%w: confirmed prefix repeats outpoint %v", ErrDoubleSpend, op)
			}
			l.prefixOutpoints[op] = true
		}
	}
	return nil
}

func (l *Ledger) deriveDeps(tx *Transaction) (predTxIDs []TxID, blocked bool, reason error) {
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		if l.prefixNullifiers[in.Nullifier] {
			return nil, true, fmt.Errorf("%w: nullifier %q", ErrSpentInPrefix, in.Nullifier)
		}
		if in.Source.Outpoint != nil {
			op := *in.Source.Outpoint
			if l.prefixOutpoints[op] {
				return nil, true, fmt.Errorf("%w: outpoint %v", ErrSpentInPrefix, op)
			}
			predTxIDs = append(predTxIDs, op.TxID)
		}
	}
	return predTxIDs, false, nil
}

func (l *Ledger) recordCandidateBookkeeping(idx int, tx *Transaction) {
	var nulls []Nullifier
	var ops []Outpoint
	spends := make(map[BalanceKey]int64)
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		nulls = append(nulls, in.Nullifier)
		if in.Source.Outpoint != nil {
			ops = append(ops, *in.Source.Outpoint)
		}
	}
	for _, s := range tx.Spends {
		spends[BalanceKey{Issuer: s.Issuer, Activity: s.Activity}] += s.Cost
	}
	l.nullifierUse[idx] = nulls
	l.outpointUse[idx] = ops
	l.spendByKey[idx] = spends
}

// N returns the number of candidate transactions in canonical order.
func (l *Ledger) N() int { return len(l.candidates) }

// CandidateTx returns the candidate transaction at canonical index i.
func (l *Ledger) CandidateTx(i int) Transaction { return l.candidates[i].tx }

// IsBlocked reports whether candidate i conflicts with the confirmed prefix
// and can therefore never be selected.
func (l *Ledger) IsBlocked(i int) bool { return l.candidates[i].blocked }

// BlockReason returns the prefix-conflict reason for candidate i.
func (l *Ledger) BlockReason(i int) error { return l.candidates[i].blockReason }
