// Package receivechain exposes the receive-chain API under its own package
// name. The implementation lives in the module root package; this package
// re-exports it so decryption entry points can import either location.
package receivechain

import (
	netsec "example.org/netsec/q0031"
)

const (
	MaxSkippedKeysPerChain = netsec.MaxSkippedKeysPerChain
	MaxSkip                = netsec.MaxSkip
	DefaultMaxSkippedKeys  = netsec.DefaultMaxSkippedKeys
	ChainKeySize           = netsec.ChainKeySize
	MessageKeySize         = netsec.MessageKeySize
)

var (
	ErrSkipLimitExceeded     = netsec.ErrSkipLimitExceeded
	ErrTooManySkippedKeys    = netsec.ErrTooManySkippedKeys
	ErrDuplicateMessage      = netsec.ErrDuplicateMessage
	ErrReplay                = netsec.ErrReplay
	ErrOldMessage            = netsec.ErrOldMessage
	ErrInvalidChainKey       = netsec.ErrInvalidChainKey
	ErrInvalidRatchetKey     = netsec.ErrInvalidRatchetKey
	ErrInvalidMaxSkippedKeys = netsec.ErrInvalidMaxSkippedKeys
	ErrInvalidNonce          = netsec.ErrInvalidNonce
	ErrInvalidCiphertext     = netsec.ErrInvalidCiphertext
	ErrNilOpener             = netsec.ErrNilOpener
	ErrNilStore              = netsec.ErrNilStore
	ErrInvalidMessageKey     = netsec.ErrInvalidMessageKey
	ErrSequenceOverflow      = netsec.ErrSequenceOverflow
	ErrInvalidSequence       = netsec.ErrInvalidSequence
)

type (
	Sequence           = netsec.Sequence
	OpenFunc           = netsec.OpenFunc
	UseKeyFunc         = netsec.UseKeyFunc
	ReceiveChain       = netsec.ReceiveChain
	ReceiveChainOption = netsec.ReceiveChainOption
	Option             = netsec.Option
	Chain              = netsec.Chain
	SkippedKeyID       = netsec.SkippedKeyID
	SkippedKeyStore    = netsec.SkippedKeyStore
	SenderChain        = netsec.SenderChain
	SendingChain       = netsec.SendingChain
)

func NewReceiveChain(
	ratchetPublicKey, chainKey []byte,
	options ...ReceiveChainOption,
) (*ReceiveChain, error) {
	return netsec.NewReceiveChain(ratchetPublicKey, chainKey, options...)
}

func MustNewReceiveChain(
	ratchetPublicKey, chainKey []byte,
	options ...ReceiveChainOption,
) *ReceiveChain {
	return netsec.MustNewReceiveChain(ratchetPublicKey, chainKey, options...)
}

func New(
	ratchetPublicKey, chainKey []byte,
	options ...ReceiveChainOption,
) (*ReceiveChain, error) {
	return netsec.New(ratchetPublicKey, chainKey, options...)
}

func NewChain(
	ratchetPublicKey, chainKey []byte,
	options ...ReceiveChainOption,
) (*ReceiveChain, error) {
	return netsec.NewChain(ratchetPublicKey, chainKey, options...)
}

func WithMaxSkippedKeys(maxSkippedKeys int) ReceiveChainOption {
	return netsec.WithMaxSkippedKeys(maxSkippedKeys)
}

func WithMaxSkip(maxSkippedKeys int) ReceiveChainOption {
	return netsec.WithMaxSkip(maxSkippedKeys)
}

func WithSkippedKeyStore(store *SkippedKeyStore) ReceiveChainOption {
	return netsec.WithSkippedKeyStore(store)
}

func NewSkippedKeyStore() *SkippedKeyStore {
	return netsec.NewSkippedKeyStore()
}

func NewSkippedKeyID(ratchetPublicKey []byte, sequence uint64) SkippedKeyID {
	return netsec.NewSkippedKeyID(ratchetPublicKey, sequence)
}

func NormalizeSequence(sequence any) (uint64, error) {
	return netsec.NormalizeSequence(sequence)
}

func NewSenderChain(chainKey []byte) (*SenderChain, error) {
	return netsec.NewSenderChain(chainKey)
}

func MustNewSenderChain(chainKey []byte) *SenderChain {
	return netsec.MustNewSenderChain(chainKey)
}

func NewSendingChain(chainKey []byte) (*SenderChain, error) {
	return netsec.NewSendingChain(chainKey)
}

func DeriveMessageKey(chainKey []byte) []byte {
	return netsec.DeriveMessageKey(chainKey)
}

func DeriveNextChainKey(chainKey []byte) []byte {
	return netsec.DeriveNextChainKey(chainKey)
}

func StepChainKey(chainKey []byte) (messageKey, nextChainKey []byte) {
	return netsec.StepChainKey(chainKey)
}

func ValidateChainKey(chainKey []byte) error {
	return netsec.ValidateChainKey(chainKey)
}

func OpenAESGCM(messageKey, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	return netsec.OpenAESGCM(messageKey, nonce, ciphertext, additionalData)
}

func SealAESGCM(messageKey, nonce, plaintext, additionalData []byte) ([]byte, error) {
	return netsec.SealAESGCM(messageKey, nonce, plaintext, additionalData)
}
