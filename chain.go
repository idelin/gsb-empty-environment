package netsec

import "math/big"

// ChainID identifies a blockchain network. Replays across different
// ChainID values are rejected by the Service.
type ChainID uint64

// TxStatus is the on-chain execution status of a transaction.
type TxStatus int

const (
	TxStatusPending TxStatus = iota
	TxStatusConfirmed
	TxStatusFailed
)

func (s TxStatus) String() string {
	switch s {
	case TxStatusConfirmed:
		return "confirmed"
	case TxStatusFailed:
		return "failed"
	default:
		return "pending"
	}
}

// Receipt is the on-chain read-back of a transaction.
type Receipt struct {
	ChainID       ChainID
	TxHash        string
	From          string
	To            string
	Amount        *big.Int
	Nonce         uint64
	Status        TxStatus
	Confirmations uint64
}

// ChainClient abstracts the chain access the idempotency service needs.
// Implementations must be safe for concurrent use.
type ChainClient interface {
	// GetReceipt returns (nil, nil) when the hash is unknown on chainID.
	GetReceipt(chainID ChainID, txHash string) (*Receipt, error)
	// SendRawTransaction broadcasts a signed transaction and returns its hash.
	SendRawTransaction(chainID ChainID, signedTx []byte) (string, error)
}
