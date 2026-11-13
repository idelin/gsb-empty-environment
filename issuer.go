package netsec

import (
	"context"
	"crypto/rsa"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// PrivateKeyOperation must return blinded^d mod N. The host owns d and must not
// expose it to this package.
type PrivateKeyOperation func(blinded *big.Int) (*big.Int, error)

// ContextPrivateKeyOperation is the cancellable form of PrivateKeyOperation.
type ContextPrivateKeyOperation func(ctx context.Context, blinded *big.Int) (*big.Int, error)

type IssuerConfig struct {
	N                         *big.Int
	E                         int
	Sign                      PrivateKeyOperation
	SignContext               ContextPrivateKeyOperation
	Verifier                  TicketVerifier
	VerifyTicket              func(context.Context, TicketID, ClaimDigest, time.Time) (TicketPolicy, error)
	Store                     RedemptionStore
	Now                       func() time.Time
	SkipSignatureVerification bool
}

type keyedMutex struct {
	locks sync.Map
}

func (m *keyedMutex) lock(id TicketID) func() {
	value, _ := m.locks.LoadOrStore(id, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// Issuer is a redemption transaction coordinator. It performs no private-key
// arithmetic itself; all private-key work is delegated to the configured
// operation callback.
type Issuer struct {
	n          *big.Int
	e          int
	eBig       *big.Int
	sign       PrivateKeyOperation
	signCtx    ContextPrivateKeyOperation
	verifier   TicketVerifier
	store      RedemptionStore
	now        func() time.Time
	skipVerify bool

	locks keyedMutex
}

type Redeemer = Issuer

func NewIssuer(cfg IssuerConfig) (*Issuer, error) {
	if cfg.N == nil || cfg.N.Sign() <= 0 || cfg.N.Cmp(big.NewInt(1)) <= 0 {
		return nil, fmt.Errorf("%w: modulus N must be greater than one", ErrInvalidConfig)
	}
	if cfg.E < 1 {
		return nil, fmt.Errorf("%w: public exponent e must be positive", ErrInvalidConfig)
	}
	if cfg.Sign == nil && cfg.SignContext == nil {
		return nil, ErrMissingSigner
	}
	verifier := cfg.Verifier
	if verifier == nil && cfg.VerifyTicket != nil {
		verifier = TicketVerifierFunc(cfg.VerifyTicket)
	}
	if verifier == nil {
		return nil, ErrMissingVerifier
	}
	if cfg.Store == nil {
		return nil, ErrMissingStore
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Issuer{
		n:          new(big.Int).Set(cfg.N),
		e:          cfg.E,
		eBig:       big.NewInt(int64(cfg.E)),
		sign:       cfg.Sign,
		signCtx:    cfg.SignContext,
		verifier:   verifier,
		store:      cfg.Store,
		now:        now,
		skipVerify: cfg.SkipSignatureVerification,
	}, nil
}

func NewIssuerWithParts(n *big.Int, e int, sign PrivateKeyOperation, verifier TicketVerifier, store RedemptionStore) (*Issuer, error) {
	return NewIssuer(IssuerConfig{
		N:        n,
		E:        e,
		Sign:     sign,
		Verifier: verifier,
		Store:    store,
	})
}

func NewIssuerFromRSA(publicKey *rsa.PublicKey, sign PrivateKeyOperation, verifier TicketVerifier, store RedemptionStore) (*Issuer, error) {
	if publicKey == nil {
		return nil, fmt.Errorf("%w: nil RSA public key", ErrInvalidConfig)
	}
	return NewIssuerWithParts(publicKey.N, publicKey.E, sign, verifier, store)
}

func (i *Issuer) Modulus() *big.Int {
	return new(big.Int).Set(i.n)
}

func (i *Issuer) PublicExponent() int {
	return i.e
}

// Redeem performs the two-commit redemption protocol for one request.
func (i *Issuer) Redeem(ctx context.Context, request RedemptionRequest) (RedemptionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := i.validateRequest(request); err != nil {
		return RedemptionResult{}, err
	}
	request.Blind = new(big.Int).Set(request.Blind)
	blindDigest := DigestBlind(request.Blind)

	existing, found, err := i.store.Lookup(ctx, request.TicketID, request.RequestID)
	if err != nil {
		return RedemptionResult{}, err
	}
	if found {
		return i.resumeExisting(ctx, request, existing, blindDigest)
	}

	now := i.now()
	policy, err := i.verifier.VerifyTicket(ctx, request.TicketID, request.ClaimDigest, now)
	if err != nil {
		return RedemptionResult{}, fmt.Errorf("netsec: ticket verification failed: %w", err)
	}
	if err := validatePolicy(policy, now); err != nil {
		return RedemptionResult{}, err
	}
	if err := checkContext(ctx); err != nil {
		return RedemptionResult{}, err
	}

	unlock := i.locks.lock(request.TicketID)
	defer unlock()

	reserved, err := i.store.Reserve(ctx, Reservation{
		TicketID:       request.TicketID,
		RequestID:      request.RequestID,
		ClaimDigest:    request.ClaimDigest,
		BlindDigest:    blindDigest,
		MaxRedemptions: policy.MaxRedemptions,
	})
	if err != nil {
		return RedemptionResult{}, err
	}
	if err := matchReservation(request, reserved.Record, blindDigest); err != nil {
		return RedemptionResult{}, err
	}
	if reserved.Record.Sequence < 1 {
		return RedemptionResult{}, fmt.Errorf("%w: invalid sequence", ErrStoreResultUnavailable)
	}
	if reserved.Created && reserved.Record.Sequence > policy.MaxRedemptions {
		return RedemptionResult{}, fmt.Errorf("%w: sequence exceeds policy", ErrStoreResultUnavailable)
	}

	if reserved.Record.Signature != nil {
		return i.resultFromRecord(request, reserved.Record, !reserved.Created)
	}

	return i.completeReservation(ctx, request, reserved.Record, blindDigest, !reserved.Created)
}

func (i *Issuer) resumeExisting(ctx context.Context, request RedemptionRequest, record Record, blindDigest BlindDigest) (RedemptionResult, error) {
	if err := matchReservation(request, record, blindDigest); err != nil {
		return RedemptionResult{}, err
	}
	if record.Sequence < 1 {
		return RedemptionResult{}, fmt.Errorf("%w: invalid sequence", ErrStoreResultUnavailable)
	}
	if record.Signature != nil {
		return i.resultFromRecord(request, record, true)
	}

	unlock := i.locks.lock(request.TicketID)
	defer unlock()

	current, found, err := i.store.Lookup(ctx, request.TicketID, request.RequestID)
	if err != nil {
		return RedemptionResult{}, err
	}
	if !found {
		return RedemptionResult{}, fmt.Errorf("%w: request %q", ErrReservationNotFound, request.RequestID)
	}
	if err := matchReservation(request, current, blindDigest); err != nil {
		return RedemptionResult{}, err
	}
	if current.Sequence < 1 {
		return RedemptionResult{}, fmt.Errorf("%w: invalid sequence", ErrStoreResultUnavailable)
	}
	if current.Signature != nil {
		return i.resultFromRecord(request, current, true)
	}
	return i.completeReservation(ctx, request, current, blindDigest, true)
}

func (i *Issuer) completeReservation(ctx context.Context, request RedemptionRequest, record Record, blindDigest BlindDigest, reused bool) (RedemptionResult, error) {
	if err := checkContext(ctx); err != nil {
		return RedemptionResult{}, err
	}
	signature, err := i.performPrivateOperation(ctx, request.Blind)
	if err != nil {
		return RedemptionResult{}, err
	}
	if err := i.validateSignature(request.Blind, signature); err != nil {
		return RedemptionResult{}, err
	}

	if err := i.store.CommitSignature(ctx, Completion{
		TicketID:    request.TicketID,
		RequestID:   request.RequestID,
		BlindDigest: blindDigest,
		Signature:   signature.Bytes(),
	}); err != nil {
		return RedemptionResult{}, err
	}

	record.Signature = signature.Bytes()
	return makeResult(request, record, signature, reused), nil
}

func (i *Issuer) resultFromRecord(request RedemptionRequest, record Record, reused bool) (RedemptionResult, error) {
	signature := new(big.Int).SetBytes(record.Signature)
	if err := i.validateSignature(request.Blind, signature); err != nil {
		return RedemptionResult{}, fmt.Errorf("%w: stored signature: %v", ErrInvalidSignature, err)
	}
	return makeResult(request, record, signature, reused), nil
}

func matchReservation(request RedemptionRequest, record Record, blindDigest BlindDigest) error {
	if record.TicketID != request.TicketID || record.RequestID != request.RequestID {
		return ErrStoreResultUnavailable
	}
	if record.ClaimDigest != request.ClaimDigest {
		return fmt.Errorf("%w: request %q was committed for another claim digest", ErrRequestConflict, request.RequestID)
	}
	if !BlindDigestEqual(record.BlindDigest, blindDigest) {
		return fmt.Errorf("%w: request %q", ErrBlindMismatch, request.RequestID)
	}
	return nil
}

// RedeemBlind is the same transaction with only the blind signature returned.
func (i *Issuer) RedeemBlind(ctx context.Context, request RedemptionRequest) (*big.Int, error) {
	result, err := i.Redeem(ctx, request)
	if err != nil {
		return nil, err
	}
	return new(big.Int).Set(result.Signature), nil
}

// Issue is a convenience alias for RedeemBlind.
func (i *Issuer) Issue(ctx context.Context, request RedemptionRequest) (*big.Int, error) {
	return i.RedeemBlind(ctx, request)
}

func (i *Issuer) Lookup(ctx context.Context, ticketID TicketID, requestID string) (Record, bool, error) {
	return i.store.Lookup(ctx, ticketID, requestID)
}

func (i *Issuer) validateRequest(request RedemptionRequest) error {
	if request.RequestID == "" {
		return fmt.Errorf("%w: empty request ID", ErrInvalidRequest)
	}
	return ValidateBlind(request.Blind, i.n)
}

func validatePolicy(policy TicketPolicy, now time.Time) error {
	if policy.MaxRedemptions < MinRedemptions || policy.MaxRedemptions > MaxRedemptions {
		return fmt.Errorf(
			"%w: max redemptions must be between %d and %d",
			ErrInvalidPolicy,
			MinRedemptions,
			MaxRedemptions,
		)
	}
	if policy.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: missing expiry", ErrInvalidPolicy)
	}
	if !now.Before(policy.ExpiresAt) {
		return ErrTicketExpired
	}
	return nil
}

func (i *Issuer) performPrivateOperation(ctx context.Context, blinded *big.Int) (*big.Int, error) {
	if i.signCtx != nil {
		signature, err := i.signCtx(ctx, new(big.Int).Set(blinded))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSignerFailed, err)
		}
		if signature == nil {
			return nil, fmt.Errorf("%w: signer returned no signature", ErrSignerFailed)
		}
		return signature, nil
	}

	signature, err := i.sign(new(big.Int).Set(blinded))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignerFailed, err)
	}
	if signature == nil {
		return nil, fmt.Errorf("%w: signer returned no signature", ErrSignerFailed)
	}
	return signature, nil
}

func (i *Issuer) validateSignature(blinded, signature *big.Int) error {
	if signature == nil {
		return fmt.Errorf("%w: nil signature", ErrInvalidSignature)
	}
	if signature.Sign() <= 0 || signature.Cmp(i.n) >= 0 {
		return fmt.Errorf("%w: signature outside 0 < s < N", ErrInvalidSignature)
	}
	if i.skipVerify {
		return nil
	}
	check := new(big.Int).Exp(signature, i.eBig, i.n)
	if check.Cmp(blinded) != 0 {
		return fmt.Errorf("%w: signature does not match the blinded value", ErrInvalidSignature)
	}
	return nil
}

func makeResult(request RedemptionRequest, record Record, signature *big.Int, reused bool) RedemptionResult {
	return RedemptionResult{
		TicketID:    request.TicketID,
		ClaimDigest: request.ClaimDigest,
		RequestID:   request.RequestID,
		Sequence:    record.Sequence,
		BlindDigest: record.BlindDigest,
		Signature:   new(big.Int).Set(signature),
		Reused:      reused,
	}
}
