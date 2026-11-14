package netsec

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Policy controls finality and event-kind acceptance.
type Policy struct {
	// MinConfirmations is head-height+1. Zero is normalized to one.
	MinConfirmations uint64
	// AllowUnfinalized permits releasing before the light client's finalized
	// height. It weakens reorg safety and should only be used with an explicit
	// application-level rollback mechanism.
	AllowUnfinalized bool
	// AcceptedKinds lists event kinds that may authorize a release. An empty
	// list accepts only EventLock.
	AcceptedKinds []EventKind
}

// DefaultPolicy requires one confirmation, finality, and lock events only.
func DefaultPolicy() Policy {
	return Policy{
		MinConfirmations: 1,
		AcceptedKinds:    []EventKind{EventLock},
	}
}

func (p Policy) normalized() (Policy, error) {
	if p.MinConfirmations == 0 {
		p.MinConfirmations = 1
	}
	if len(p.AcceptedKinds) == 0 {
		p.AcceptedKinds = []EventKind{EventLock}
	}
	kinds := make([]EventKind, len(p.AcceptedKinds))
	for i, kind := range p.AcceptedKinds {
		if kind != EventLock && kind != EventBurn {
			return Policy{}, fmt.Errorf("%w: %q", ErrUnknownEventKind, kind)
		}
		kinds[i] = kind
	}
	p.AcceptedKinds = kinds
	return p, nil
}

// Allows reports whether kind may authorize a release under p.
func (p Policy) Allows(kind EventKind) bool {
	if len(p.AcceptedKinds) == 0 {
		return kind == EventLock
	}
	for _, accepted := range p.AcceptedKinds {
		if kind == accepted {
			return true
		}
	}
	return false
}

// Config configures a Verifier.
type Config struct {
	SourceChain      ChainID
	DestinationChain ChainID
	// BridgeID optionally binds proofs to one bridge deployment on the
	// destination chain. It must match Event.BridgeID.
	BridgeID    string
	LightClient *LightClient
	Policy      Policy
	Store       Store
	// Clock is optional and primarily useful for deterministic tests.
	Clock func() time.Time
}

// Verifier authorizes target-chain releases from authenticated source events.
// Process is the security boundary that consumes a message exactly once.
type Verifier struct {
	sourceChain ChainID
	destination ChainID
	bridgeID    string
	lightClient *LightClient
	policy      Policy
	store       Store
	clock       func() time.Time
}

// Release is a release authorization returned by Verify or Process.
type Release struct {
	MessageKey       Hash
	SourceChain      ChainID
	DestinationChain ChainID
	BridgeID         string
	BlockHeight      uint64
	BlockHash        Hash
	TxHash           Hash
	TxIndex          uint32
	EventIndex       uint32
	Kind             EventKind
	Sender           []byte
	AssetID          string
	Amount           uint64
	Recipient        []byte
	Nonce            uint64
	EventHash        Hash
	ProcessedAt      time.Time
}

// NewVerifier builds a bridge verifier. The source chain must match the light
// client and differ from the destination chain.
func NewVerifier(config Config) (*Verifier, error) {
	if err := config.SourceChain.Validate(); err != nil {
		return nil, err
	}
	if err := config.DestinationChain.Validate(); err != nil {
		return nil, err
	}
	if config.SourceChain == config.DestinationChain {
		return nil, errors.New("netsec: source and destination chains are identical")
	}
	if len(config.BridgeID) > maxBridgeIDLength {
		return nil, fmt.Errorf("netsec: bridge id exceeds %d bytes", maxBridgeIDLength)
	}
	if config.LightClient == nil {
		return nil, errors.New("netsec: nil light client")
	}
	if config.LightClient.ChainID() != config.SourceChain {
		return nil, fmt.Errorf("%w: light client chain %q, configured source %q",
			ErrProofBinding, config.LightClient.ChainID(), config.SourceChain)
	}
	policy, err := config.Policy.normalized()
	if err != nil {
		return nil, err
	}
	store := config.Store
	if store == nil {
		store = NewMemoryStore()
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Verifier{
		sourceChain: config.SourceChain,
		destination: config.DestinationChain,
		bridgeID:    config.BridgeID,
		lightClient: config.LightClient,
		policy:      policy,
		store:       store,
		clock:       clock,
	}, nil
}

// SourceChain returns the configured source chain.
func (v *Verifier) SourceChain() ChainID { return v.sourceChain }

// DestinationChain returns the configured destination chain.
func (v *Verifier) DestinationChain() ChainID { return v.destination }

// BridgeID returns the configured bridge deployment identifier.
func (v *Verifier) BridgeID() string { return v.bridgeID }

// LightClient returns the underlying light client.
func (v *Verifier) LightClient() *LightClient { return v.lightClient }

// Store returns the replay store.
func (v *Verifier) Store() Store { return v.store }

// Policy returns the normalized policy.
func (v *Verifier) Policy() Policy { return v.policy }

// AddHeader is a convenience wrapper around LightClient.AddHeader.
func (v *Verifier) AddHeader(header Header, signatures []HeaderSignature) error {
	return v.lightClient.AddHeader(header, signatures)
}

// Reset installs a new trusted checkpoint on the underlying light client.
func (v *Verifier) Reset(trusted TrustedState) error {
	return v.lightClient.Reset(trusted)
}

// Verify performs all consensus, inclusion, binding, and finality checks but
// does not consume the message. Use Process for exactly-once release.
func (v *Verifier) Verify(ctx context.Context, proof MessageProof) (Release, error) {
	return v.verify(ctx, proof)
}

// Process verifies the proof and atomically consumes its unique message key.
// Exactly one concurrent call for a given source event can succeed.
func (v *Verifier) Process(ctx context.Context, proof MessageProof) (Release, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Release{}, err
	}
	if violation := v.lightClient.Violation(); violation != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrFrozen, violation)
	}
	if err := proof.Validate(); err != nil {
		return Release{}, err
	}
	if proof.DestinationChain != v.destination {
		return Release{}, fmt.Errorf("%w: proof destination %q, expected %q",
			ErrDestinationMismatch, proof.DestinationChain, v.destination)
	}
	if proof.Event.BridgeID != v.bridgeID {
		return Release{}, fmt.Errorf("%w: proof bridge %q, expected %q",
			ErrBridgeMismatch, proof.Event.BridgeID, v.bridgeID)
	}
	key := proof.MessageKey()
	existing, ok, err := v.store.Lookup(key)
	if err != nil {
		return Release{}, err
	}
	if ok {
		return Release{}, &AlreadyProcessedError{Key: key, Existing: existing}
	}

	release, err := v.verify(ctx, proof)
	if err != nil {
		return Release{}, err
	}
	release.ProcessedAt = v.clock().UTC()
	record := recordFromRelease(release)
	if err := v.store.Consume(record); err != nil {
		if errors.Is(err, ErrAlreadyProcessed) {
			var processed *AlreadyProcessedError
			if errors.As(err, &processed) {
				return Release{}, processed
			}
			if existing, found, lookupErr := v.store.Lookup(key); lookupErr == nil && found {
				return Release{}, &AlreadyProcessedError{Key: key, Existing: existing}
			}
		}
		return Release{}, err
	}
	return release, nil
}

func (v *Verifier) verify(ctx context.Context, proof MessageProof) (Release, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Release{}, err
	}
	if err := proof.Validate(); err != nil {
		return Release{}, err
	}
	if proof.DestinationChain != v.destination {
		return Release{}, fmt.Errorf("%w: proof destination %q, expected %q",
			ErrDestinationMismatch, proof.DestinationChain, v.destination)
	}
	if proof.Event.BridgeID != v.bridgeID {
		return Release{}, fmt.Errorf("%w: proof bridge %q, expected %q",
			ErrBridgeMismatch, proof.Event.BridgeID, v.bridgeID)
	}
	if !v.policy.Allows(proof.Event.Kind) {
		return Release{}, fmt.Errorf("%w: policy does not allow %q releases", ErrUnknownEventKind, proof.Event.Kind)
	}

	view, ok := v.lightClient.View(proof.BlockHeight)
	if !ok {
		if violation := v.lightClient.Violation(); violation != nil {
			return Release{}, fmt.Errorf("%w: %w", ErrFrozen, violation)
		}
		return Release{}, fmt.Errorf("%w: height %d", ErrUnknownHeader, proof.BlockHeight)
	}
	if view.Violation != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrFrozen, view.Violation)
	}
	header := view.Header
	if header.BlockHash != proof.BlockHash {
		return Release{}, fmt.Errorf("%w: height %d is %s, proof claims %s",
			ErrBlockNotCanonical, proof.BlockHeight, header.BlockHash, proof.BlockHash)
	}
	if header.ChainID != v.sourceChain || header.ChainID != proof.SourceChain {
		return Release{}, fmt.Errorf("%w: header/source chain mismatch", ErrProofBinding)
	}
	if header.Height > view.Head.Height {
		return Release{}, fmt.Errorf("%w: header is above the verified head", ErrBlockNotCanonical)
	}
	confirmations := view.Head.Height - header.Height + 1
	if confirmations < v.policy.MinConfirmations {
		return Release{}, fmt.Errorf("%w: %d confirmations, need %d",
			ErrInsufficientConfirmations, confirmations, v.policy.MinConfirmations)
	}
	if !v.policy.AllowUnfinalized && header.Height > view.FinalizedHeight {
		return Release{}, fmt.Errorf("%w: height %d is above finalized height %d",
			ErrNotFinal, header.Height, view.FinalizedHeight)
	}

	if err := VerifyMerkleProof(header.TxRoot, TxLeafHash(proof.TxHash), proof.TxProof); err != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrInvalidTxProof, err)
	}

	eventHash := proof.Event.LeafHash()
	if err := VerifyMerkleProof(header.EventRoot, eventHash, proof.EventProof); err != nil {
		return Release{}, fmt.Errorf("%w: %w: %w", ErrInvalidEventProof, ErrForgedEvent, err)
	}
	if err := ctx.Err(); err != nil {
		return Release{}, err
	}

	return releaseFromProof(proof, eventHash), nil
}

func releaseFromProof(proof MessageProof, eventHash Hash) Release {
	return Release{
		MessageKey:       proof.MessageKey(),
		SourceChain:      proof.SourceChain,
		DestinationChain: proof.DestinationChain,
		BridgeID:         proof.Event.BridgeID,
		BlockHeight:      proof.BlockHeight,
		BlockHash:        proof.BlockHash,
		TxHash:           proof.TxHash,
		TxIndex:          proof.TxIndex,
		EventIndex:       proof.EventIndex,
		Kind:             proof.Event.Kind,
		Sender:           append([]byte(nil), proof.Event.Sender...),
		AssetID:          proof.Event.AssetID,
		Amount:           proof.Event.Amount,
		Recipient:        append([]byte(nil), proof.Event.Recipient...),
		Nonce:            proof.Event.Nonce,
		EventHash:        eventHash,
	}
}

func recordFromRelease(release Release) Record {
	return Record{
		MessageKey:       release.MessageKey,
		SourceChain:      release.SourceChain,
		DestinationChain: release.DestinationChain,
		BridgeID:         release.BridgeID,
		BlockHeight:      release.BlockHeight,
		BlockHash:        release.BlockHash,
		TxHash:           release.TxHash,
		TxIndex:          release.TxIndex,
		EventIndex:       release.EventIndex,
		Kind:             release.Kind,
		Sender:           append([]byte(nil), release.Sender...),
		AssetID:          release.AssetID,
		Amount:           release.Amount,
		Recipient:        append([]byte(nil), release.Recipient...),
		Nonce:            release.Nonce,
		EventHash:        release.EventHash,
		ProcessedAt:      release.ProcessedAt,
	}
}
