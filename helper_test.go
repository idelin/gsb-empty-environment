package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"testing"
)

type testValidator struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
	power   uint64
	index   uint32
}

type blockData struct {
	header     Header
	signatures []HeaderSignature
	txs        []Hash
	events     []Event
}

type testEnv struct {
	t             *testing.T
	source        ChainID
	destination   ChainID
	validators    []testValidator
	validatorSet  ValidatorSet
	lightClient   *LightClient
	genesis       Header
	head          Header
	blocks        map[uint64]*blockData
	finalityDepth uint64
}

func newTestEnv(t *testing.T, finalityDepth uint64) *testEnv {
	t.Helper()
	const validatorCount = 4
	validators := make([]testValidator, validatorCount)
	apiValidators := make([]Validator, validatorCount)
	for i := 0; i < validatorCount; i++ {
		seed := sha256.Sum256([]byte(fmt.Sprintf("example.org/netsec/q0453/test-validator/%d", i)))
		private := ed25519.NewKeyFromSeed(seed[:])
		public := private.Public().(ed25519.PublicKey)
		validators[i] = testValidator{public: public, private: private, power: 1}
		apiValidators[i] = Validator{PublicKey: public, Power: 1}
	}
	validatorSet, err := NewValidatorSet(apiValidators)
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	for i := range validators {
		found := false
		for index, canonical := range validatorSet.Validators {
			if bytes.Equal(validators[i].public, canonical.PublicKey) {
				validators[i].index = uint32(index)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("validator %d missing from canonical set", i)
		}
	}
	genesis := Header{
		ChainID:        "source-chain",
		Height:         1,
		ParentHash:     sumParts("test-genesis-parent/v1"),
		TxRoot:         EmptyMerkleRoot(),
		EventRoot:      EmptyMerkleRoot(),
		Timestamp:      1000,
		ValidatorsHash: validatorSet.Hash(),
	}
	genesis.BlockHash = genesis.ComputeHash()
	lightClient, err := NewLightClient(TrustedState{Header: genesis, Validators: validatorSet}, finalityDepth)
	if err != nil {
		t.Fatalf("NewLightClient: %v", err)
	}
	return &testEnv{
		t:             t,
		source:        "source-chain",
		destination:   "dest-chain",
		validators:    validators,
		validatorSet:  validatorSet,
		lightClient:   lightClient,
		genesis:       genesis,
		head:          genesis,
		blocks:        map[uint64]*blockData{genesis.Height: {header: genesis}},
		finalityDepth: finalityDepth,
	}
}

func lockEvent(asset string, amount uint64, recipient string) Event {
	return Event{
		Kind:      EventLock,
		Sender:    []byte("alice"),
		AssetID:   asset,
		Amount:    amount,
		Recipient: []byte(recipient),
	}
}

func (e *testEnv) buildOn(parent Header, events []Event, timestamp int64) *blockData {
	e.t.Helper()
	height := parent.Height + 1
	txs := make([]Hash, len(events))
	normalized := make([]Event, len(events))
	for i, event := range events {
		if event.SourceChain == "" {
			event.SourceChain = e.source
		}
		if event.DestinationChain == "" {
			event.DestinationChain = e.destination
		}
		if event.Kind == "" {
			event.Kind = EventLock
		}
		txHash := hashDomainSeparated("test-tx/v1",
			parent.BlockHash[:],
			uint64Bytes(height),
			uint64Bytes(uint64(i)))
		if event.TxHash.IsZero() {
			event.TxHash = txHash
		}
		event.BlockHeight = height
		event.TxIndex = uint32(i)
		event.EventIndex = uint32(i)
		txs[i] = event.TxHash
		normalized[i] = event
	}

	txLeaves := make([]Hash, len(txs))
	for i, tx := range txs {
		txLeaves[i] = TxLeafHash(tx)
	}
	txRoot, err := BuildMerkleRoot(txLeaves)
	if err != nil {
		e.t.Fatalf("build tx root: %v", err)
	}
	eventLeaves := make([]Hash, len(normalized))
	for i, event := range normalized {
		eventLeaves[i] = event.LeafHash()
	}
	eventRoot, err := BuildMerkleRoot(eventLeaves)
	if err != nil {
		e.t.Fatalf("build event root: %v", err)
	}
	if timestamp == 0 {
		timestamp = parent.Timestamp + 1
	}
	header := Header{
		ChainID:        e.source,
		Height:         height,
		ParentHash:     parent.BlockHash,
		TxRoot:         txRoot,
		EventRoot:      eventRoot,
		Timestamp:      timestamp,
		ValidatorsHash: e.validatorSet.Hash(),
	}
	header.BlockHash = header.ComputeHash()
	return &blockData{
		header:     header,
		signatures: e.signAll(header),
		txs:        txs,
		events:     normalized,
	}
}

func (e *testEnv) signAll(header Header) []HeaderSignature {
	e.t.Helper()
	signatures := make([]HeaderSignature, len(e.validators))
	signBytes := header.SignBytes()
	for i, validator := range e.validators {
		signatures[i] = HeaderSignature{
			ValidatorIndex: validator.index,
			Signature:      ed25519.Sign(validator.private, signBytes[:]),
		}
	}
	return signatures
}

func (e *testEnv) appendBlock(events ...Event) *blockData {
	e.t.Helper()
	block := e.buildOn(e.head, events, 0)
	if err := e.lightClient.AddHeader(block.header, block.signatures); err != nil {
		e.t.Fatalf("AddHeader(height=%d): %v", block.header.Height, err)
	}
	e.blocks[block.header.Height] = block
	e.head = block.header
	return block
}

func (e *testEnv) submit(block *blockData) error {
	return e.lightClient.AddHeader(block.header, block.signatures)
}

func (e *testEnv) proof(block *blockData, eventIndex int) MessageProof {
	e.t.Helper()
	if eventIndex < 0 || eventIndex >= len(block.events) {
		e.t.Fatalf("event index %d out of range", eventIndex)
	}
	event := block.events[eventIndex]
	txLeaves := make([]Hash, len(block.txs))
	for i, tx := range block.txs {
		txLeaves[i] = TxLeafHash(tx)
	}
	txProof, err := BuildMerkleProof(txLeaves, uint64(event.TxIndex))
	if err != nil {
		e.t.Fatalf("build tx proof: %v", err)
	}
	eventLeaves := make([]Hash, len(block.events))
	for i, candidate := range block.events {
		eventLeaves[i] = candidate.LeafHash()
	}
	eventProof, err := BuildMerkleProof(eventLeaves, uint64(event.EventIndex))
	if err != nil {
		e.t.Fatalf("build event proof: %v", err)
	}
	return MessageProof{
		SourceChain:      event.SourceChain,
		DestinationChain: event.DestinationChain,
		BlockHeight:      block.header.Height,
		BlockHash:        block.header.BlockHash,
		TxHash:           event.TxHash,
		TxIndex:          event.TxIndex,
		EventIndex:       event.EventIndex,
		Event:            event,
		TxProof:          txProof,
		EventProof:       eventProof,
	}
}

func (e *testEnv) finalize(height uint64) {
	e.t.Helper()
	target := height + e.finalityDepth
	for e.head.Height < target {
		e.appendBlock()
	}
}

func (e *testEnv) newVerifier(policy Policy, store Store, destination ChainID) *Verifier {
	e.t.Helper()
	return e.newVerifierWithBridge(policy, store, destination, "")
}

func (e *testEnv) newVerifierWithBridge(policy Policy, store Store, destination ChainID, bridgeID string) *Verifier {
	e.t.Helper()
	if destination == "" {
		destination = e.destination
	}
	verifier, err := NewVerifier(Config{
		SourceChain:      e.source,
		DestinationChain: destination,
		BridgeID:         bridgeID,
		LightClient:      e.lightClient,
		Policy:           policy,
		Store:            store,
	})
	if err != nil {
		e.t.Fatalf("NewVerifier: %v", err)
	}
	return verifier
}

func cloneProof(proof MessageProof) MessageProof {
	proof.Event.Sender = append([]byte(nil), proof.Event.Sender...)
	proof.Event.Recipient = append([]byte(nil), proof.Event.Recipient...)
	proof.TxProof.Siblings = append([]Hash(nil), proof.TxProof.Siblings...)
	proof.EventProof.Siblings = append([]Hash(nil), proof.EventProof.Siblings...)
	return proof
}

type escrow struct {
	locked   uint64
	released uint64
}

func (e *escrow) deposit(t *testing.T, amount uint64) {
	t.Helper()
	if ^uint64(0)-e.locked < amount {
		t.Fatalf("escrow overflow")
	}
	e.locked += amount
}

func (e *escrow) release(t *testing.T, amount uint64) {
	t.Helper()
	if amount > e.locked-e.released {
		t.Fatalf("unbacked release: releasing %d with %d still locked", amount, e.locked-e.released)
	}
	e.released += amount
}
