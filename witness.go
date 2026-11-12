package taskpkg

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
)

// Domain separation prefixes. They keep signatures over distinct record
// shapes from being interchangeable.
const (
	domainOperation    = "taskpkg/op/v1:"
	domainConfirmation = "taskpkg/confirmation/v1:"
	domainCommitment   = "taskpkg/commitment/v1:"
)

// Operation is the unit of sensitive work submitted by the operations host.
type Operation struct {
	// Subject identifies the acting principal, e.g. an admin account ID.
	Subject string
	// Summary is a stable, self-contained description of the sensitive action.
	Summary string
	// Payload optionally carries additional operation data that must be bound
	// into the digest.
	Payload []byte
}

// CommitmentRequest is sent to the remote witness. Sequence is assigned by
// the local monotonic counter before the witness is contacted.
type CommitmentRequest struct {
	Operation       Operation
	Sequence        uint64
	PriorCommit     []byte
	OperationDigest []byte
}

// Confirmation is the signed statement returned by the trusted witness.
type Confirmation struct {
	Sequence        uint64
	Subject         string
	OperationDigest []byte
	PriorCommit     []byte
	Signature       []byte
}

func putLengthPrefixed(dst []byte, tag string, data []byte) []byte {
	dst = append(dst, tag...)
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	dst = append(dst, lenBuf[:]...)
	return append(dst, data...)
}

// OperationDigest returns the digest bound into a witness confirmation.
func OperationDigest(op Operation) []byte {
	var buf []byte
	buf = putLengthPrefixed(buf, domainOperation, []byte(op.Subject))
	buf = putLengthPrefixed(buf, domainOperation, []byte(op.Summary))
	buf = putLengthPrefixed(buf, domainOperation, op.Payload)
	sum := sha256Sum(buf)
	return sum[:]
}

// confirmationSigningBytes renders the exact byte string the witness signs.
// Sequence is fixed-width and therefore unambiguous; every variable-length
// field is length-prefixed.
func confirmationSigningBytes(c Confirmation) []byte {
	var buf []byte
	buf = append(buf, domainConfirmation...)
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], c.Sequence)
	buf = append(buf, seqBuf[:]...)
	buf = putLengthPrefixed(buf, domainConfirmation, []byte(c.Subject))
	buf = putLengthPrefixed(buf, domainConfirmation, c.OperationDigest)
	buf = putLengthPrefixed(buf, domainConfirmation, c.PriorCommit)
	return buf
}

// EntryCommitment is the local hash-chain commitment persisted for one
// committed entry. It covers the full verified confirmation, so any later
// modification of the log is detectable.
func EntryCommitment(c Confirmation) []byte {
	var buf []byte
	buf = putLengthPrefixed(buf, domainCommitment, confirmationSigningBytes(c))
	buf = putLengthPrefixed(buf, domainCommitment, c.Signature)
	sum := sha256Sum(buf)
	return sum[:]
}

// Witness is the trusted remote signer.
type Witness interface {
	// RequestConfirmation issues the signed confirmation for one operation.
	RequestConfirmation(ctx context.Context, req CommitmentRequest) (Confirmation, error)
	// PublicKey returns the Ed25519 public key used to verify confirmations.
	PublicKey() ed25519.PublicKey
}

// LocalWitness keeps the Ed25519 private key inside the process. It performs
// real signing and is suitable as the in-process implementation behind an
// HTTP callback; in production the key would live in an HSM/TEE and signing
// would happen remotely.
type LocalWitness struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

// NewLocalWitness creates a witness from an Ed25519 private key.
func NewLocalWitness(private ed25519.PrivateKey) *LocalWitness {
	publicKey := private.Public().(ed25519.PublicKey)
	return &LocalWitness{publicKey: publicKey, privateKey: private}
}

// PublicKey implements Witness.
func (w *LocalWitness) PublicKey() ed25519.PublicKey {
	return w.publicKey
}

// RequestConfirmation implements Witness with a real Ed25519 signature.
func (w *LocalWitness) RequestConfirmation(_ context.Context, req CommitmentRequest) (Confirmation, error) {
	confirmation := Confirmation{
		Sequence:        req.Sequence,
		Subject:         req.Operation.Subject,
		OperationDigest: req.OperationDigest,
		PriorCommit:     req.PriorCommit,
	}
	confirmation.Signature = ed25519.Sign(w.privateKey, confirmationSigningBytes(confirmation))
	return confirmation, nil
}

// VerifyConfirmation cryptographically validates a confirmation and checks
// that every field is bound to the expected commitment request.
func VerifyConfirmation(publicKey ed25519.PublicKey, req CommitmentRequest, c Confirmation) error {
	if c.Sequence != req.Sequence ||
		c.Subject != req.Operation.Subject ||
		!constantTimeEqual(c.OperationDigest, req.OperationDigest) ||
		!constantTimeEqual(c.PriorCommit, req.PriorCommit) {
		return ErrBadSignature
	}
	if !ed25519.Verify(publicKey, confirmationSigningBytes(c), c.Signature) {
		return ErrBadSignature
	}
	return nil
}
