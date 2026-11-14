// Package netsec provides an idempotent submission component for on-chain
// payment services. A business order is bound to exactly one
// (chain, account, amount, nonce, tx hash) tuple so that duplicate
// submissions, nonce reuse, signature replacement and cross-chain replays
// cannot cause a second payment.
package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
)

var (
	// ErrOrderConflict is returned when an order ID is resubmitted with
	// different business fields (chain/account/amount/nonce).
	ErrOrderConflict = errors.New("txidem: order resubmitted with conflicting fields")
	// ErrSignatureReplaced is returned when an order ID is resubmitted with
	// a different transaction hash (re-signed / mutated signature bytes).
	ErrSignatureReplaced = errors.New("txidem: signed transaction replaced for existing order")
	// ErrNonceReused is returned when (chain, account, nonce) is already
	// bound to a different order.
	ErrNonceReused = errors.New("txidem: nonce already used by another order")
	// ErrTxHashReused is returned when a tx hash is already bound to a
	// different order on the same chain.
	ErrTxHashReused = errors.New("txidem: transaction hash already bound to another order")
	// ErrCrossChainReplay is returned when a tx hash already seen on one
	// chain is submitted for a different chain.
	ErrCrossChainReplay = errors.New("txidem: transaction hash replayed across chains")
	// ErrChainStateMismatch is returned when the on-chain receipt does not
	// match the bound order fields.
	ErrChainStateMismatch = errors.New("txidem: on-chain state does not match bound order")
	// ErrTxFailed is returned by Refresh when the on-chain status is failed.
	ErrTxFailed = errors.New("txidem: transaction failed on chain")
	// ErrOrderNotFound is returned when referencing an unknown order.
	ErrOrderNotFound = errors.New("txidem: order not found")
)

// Order is a business payment order. The tuple
// (ID, ChainID, From, To, Amount, Nonce, TxHash) is bound atomically on
// first submission and can never be rebound.
type Order struct {
	ID       string   // business order ID, the idempotency key
	ChainID  ChainID  // target chain
	From     string   // sender account
	To       string   // recipient account
	Amount   *big.Int // transfer amount
	Nonce    uint64   // sender nonce
	TxHash   string   // hash of the signed transaction to broadcast
	SignedTx []byte   // raw signed transaction
}

func (o Order) validate() error {
	if o.ID == "" {
		return errors.New("txidem: order ID is required")
	}
	if o.From == "" || o.To == "" {
		return errors.New("txidem: from/to accounts are required")
	}
	if o.Amount == nil || o.Amount.Sign() <= 0 {
		return errors.New("txidem: amount must be positive")
	}
	if o.TxHash == "" || len(o.SignedTx) == 0 {
		return errors.New("txidem: signed transaction and its hash are required")
	}
	return nil
}

// Record is the stored binding of an order to its on-chain transaction.
type Record struct {
	Order   Order
	Receipt Receipt
}

type nonceKey struct {
	chain ChainID
	from  string
	nonce uint64
}

type hashRef struct {
	chain   ChainID
	orderID string
}

// Service provides idempotent transaction submission. It is safe for
// concurrent use.
type Service struct {
	mu      sync.Mutex
	chain   ChainClient
	byOrder map[string]*Record
	byNonce map[nonceKey]string
	byHash  map[string]hashRef
}

// NewService creates a Service backed by the given chain client.
func NewService(chain ChainClient) *Service {
	return &Service{
		chain:   chain,
		byOrder: make(map[string]*Record),
		byNonce: make(map[nonceKey]string),
		byHash:  make(map[string]hashRef),
	}
}

// Submit pays the order exactly once. Re-submitting the identical order
// returns the stored record without broadcasting again. Any deviation
// (different hash, nonce, amount, chain, accounts) is rejected.
func (s *Service) Submit(o Order) (*Record, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.byOrder[o.ID]; ok {
		return rec, s.checkResubmit(rec, o)
	}

	nk := nonceKey{chain: o.ChainID, from: o.From, nonce: o.Nonce}
	if _, used := s.byNonce[nk]; used {
		return nil, fmt.Errorf("%w: chain=%d account=%s nonce=%d", ErrNonceReused, o.ChainID, o.From, o.Nonce)
	}

	if ref, seen := s.byHash[o.TxHash]; seen {
		if ref.chain != o.ChainID {
			return nil, fmt.Errorf("%w: hash=%s seen on chain=%d, submitted for chain=%d",
				ErrCrossChainReplay, o.TxHash, ref.chain, o.ChainID)
		}
		return nil, fmt.Errorf("%w: hash=%s bound to order=%s", ErrTxHashReused, o.TxHash, ref.orderID)
	}

	// Pre-submit chain check: if the hash is already on chain (e.g. an
	// attacker replayed the raw signed tx), adopt it instead of paying again.
	if rc, err := s.chain.GetReceipt(o.ChainID, o.TxHash); err != nil {
		return nil, fmt.Errorf("txidem: pre-submit chain check: %w", err)
	} else if rc != nil {
		if err := matchReceipt(o, rc); err != nil {
			return nil, err
		}
		return s.bind(o, *rc), nil
	}

	hash, err := s.chain.SendRawTransaction(o.ChainID, o.SignedTx)
	if err != nil {
		return nil, fmt.Errorf("txidem: broadcast: %w", err)
	}
	if hash != o.TxHash {
		return nil, fmt.Errorf("%w: broadcast returned hash=%s, bound hash=%s",
			ErrChainStateMismatch, hash, o.TxHash)
	}

	return s.bind(o, Receipt{
		ChainID: o.ChainID,
		TxHash:  o.TxHash,
		From:    o.From,
		To:      o.To,
		Amount:  new(big.Int).Set(o.Amount),
		Nonce:   o.Nonce,
		Status:  TxStatusPending,
	}), nil
}

// checkResubmit verifies that a repeated submission carries the identical
// binding. The stored record is returned on success.
func (s *Service) checkResubmit(rec *Record, o Order) error {
	b := rec.Order
	if o.TxHash != b.TxHash {
		return fmt.Errorf("%w: order=%s bound hash=%s, got=%s", ErrSignatureReplaced, o.ID, b.TxHash, o.TxHash)
	}
	if o.ChainID != b.ChainID || o.From != b.From || o.To != b.To ||
		o.Nonce != b.Nonce || o.Amount.Cmp(b.Amount) != 0 {
		return fmt.Errorf("%w: order=%s", ErrOrderConflict, o.ID)
	}
	return nil
}

// bind stores the order binding. Caller must hold s.mu.
func (s *Service) bind(o Order, rc Receipt) *Record {
	rec := &Record{Order: o, Receipt: rc}
	s.byOrder[o.ID] = rec
	s.byNonce[nonceKey{chain: o.ChainID, from: o.From, nonce: o.Nonce}] = o.ID
	s.byHash[o.TxHash] = hashRef{chain: o.ChainID, orderID: o.ID}
	return rec
}

// matchReceipt verifies an on-chain receipt against the bound order fields.
func matchReceipt(o Order, rc *Receipt) error {
	if rc.ChainID != o.ChainID || rc.From != o.From || rc.To != o.To ||
		rc.Nonce != o.Nonce || rc.Amount == nil || rc.Amount.Cmp(o.Amount) != 0 {
		return fmt.Errorf("%w: order=%s hash=%s", ErrChainStateMismatch, o.ID, o.TxHash)
	}
	return nil
}

// Refresh reads back the on-chain status and confirmation count for a
// submitted order, updating the stored record. It returns ErrTxFailed
// (together with the receipt) when the transaction failed on chain.
func (s *Service) Refresh(orderID string) (*Receipt, error) {
	s.mu.Lock()
	rec, ok := s.byOrder[orderID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}

	rc, err := s.chain.GetReceipt(rec.Order.ChainID, rec.Order.TxHash)
	if err != nil {
		return nil, fmt.Errorf("txidem: refresh: %w", err)
	}
	if rc == nil {
		return nil, fmt.Errorf("txidem: refresh: hash=%s not found on chain=%d", rec.Order.TxHash, rec.Order.ChainID)
	}
	if err := matchReceipt(rec.Order, rc); err != nil {
		return nil, err
	}

	s.mu.Lock()
	rec.Receipt = *rc
	s.mu.Unlock()

	if rc.Status == TxStatusFailed {
		return rc, fmt.Errorf("%w: order=%s hash=%s", ErrTxFailed, orderID, rc.TxHash)
	}
	return rc, nil
}

// Get returns the stored record for an order ID.
func (s *Service) Get(orderID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byOrder[orderID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}
	return rec, nil
}
