package netsec

import "fmt"

// MessageProof binds a source event to a block, transaction, event index, and
// destination chain. The header identified by BlockHeight/BlockHash must
// already be known to the light client and canonical.
type MessageProof struct {
	SourceChain      ChainID     `json:"source_chain"`
	DestinationChain ChainID     `json:"destination_chain"`
	BlockHeight      uint64      `json:"block_height"`
	BlockHash        Hash        `json:"block_hash"`
	TxHash           Hash        `json:"tx_hash"`
	TxIndex          uint32      `json:"tx_index"`
	EventIndex       uint32      `json:"event_index"`
	Event            Event       `json:"event"`
	TxProof          MerkleProof `json:"tx_proof"`
	EventProof       MerkleProof `json:"event_proof"`
}

// Validate checks that the proof envelope and embedded event agree on every
// binding field. It does not verify Merkle roots or consensus; Verify/Process
// do that.
func (p MessageProof) Validate() error {
	if err := p.SourceChain.Validate(); err != nil {
		return fmt.Errorf("%w: source chain: %v", ErrProofBinding, err)
	}
	if err := p.DestinationChain.Validate(); err != nil {
		return fmt.Errorf("%w: destination chain: %v", ErrProofBinding, err)
	}
	if p.BlockHash.IsZero() {
		return fmt.Errorf("%w: zero block hash", ErrProofBinding)
	}
	if p.TxHash.IsZero() {
		return fmt.Errorf("%w: zero transaction hash", ErrProofBinding)
	}
	if err := p.Event.Validate(); err != nil {
		return err
	}
	if p.SourceChain != p.Event.SourceChain {
		return fmt.Errorf("%w: source chain mismatch", ErrProofBinding)
	}
	if p.DestinationChain != p.Event.DestinationChain {
		return fmt.Errorf("%w: destination chain mismatch", ErrProofBinding)
	}
	if p.BlockHeight != p.Event.BlockHeight {
		return fmt.Errorf("%w: block height mismatch", ErrProofBinding)
	}
	if p.TxHash != p.Event.TxHash {
		return fmt.Errorf("%w: transaction hash mismatch", ErrProofBinding)
	}
	if p.TxIndex != p.Event.TxIndex {
		return fmt.Errorf("%w: transaction index mismatch", ErrProofBinding)
	}
	if p.EventIndex != p.Event.EventIndex {
		return fmt.Errorf("%w: event index mismatch", ErrProofBinding)
	}
	if p.TxProof.Index != uint64(p.TxIndex) {
		return fmt.Errorf("%w: transaction proof index mismatch", ErrProofBinding)
	}
	if p.EventProof.Index != uint64(p.EventIndex) {
		return fmt.Errorf("%w: event proof index mismatch", ErrProofBinding)
	}
	if err := p.TxProof.ValidateShape(); err != nil {
		return fmt.Errorf("%w: transaction proof: %v", ErrProofBinding, err)
	}
	if err := p.EventProof.ValidateShape(); err != nil {
		return fmt.Errorf("%w: event proof: %v", ErrProofBinding, err)
	}
	return nil
}

// MessageKey returns the unique idempotency key for the proven event.
func (p MessageProof) MessageKey() Hash {
	return MessageKey(p.SourceChain, p.DestinationChain, p.TxHash, p.EventIndex)
}
