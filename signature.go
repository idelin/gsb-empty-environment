package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

var (
	approvalSignatureDomain = []byte("netsec/social-recovery/approval/v1\x00")
	vetoSignatureDomain     = []byte("netsec/social-recovery/veto/v1\x00")
)

func appendUint64(buf *bytes.Buffer, value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	buf.Write(raw[:])
}

func appendBytes(buf *bytes.Buffer, value []byte) {
	appendUint64(buf, uint64(len(value)))
	buf.Write(value)
}

func appendString(buf *bytes.Buffer, value string) {
	appendBytes(buf, []byte(value))
}

func approvalSigningBytesForFields(
	accountID AccountID,
	recoveryID RecoveryID,
	epoch uint64,
	target Digest,
	device Digest,
	deadline int64,
	contactID ContactID,
	fingerprint Fingerprint,
) []byte {
	var buf bytes.Buffer
	buf.Write(approvalSignatureDomain)
	appendString(&buf, string(accountID))
	appendString(&buf, string(recoveryID))
	appendUint64(&buf, epoch)
	appendBytes(&buf, target[:])
	appendBytes(&buf, device[:])
	appendUint64(&buf, uint64(deadline))
	appendString(&buf, string(contactID))
	appendBytes(&buf, fingerprint[:])
	return buf.Bytes()
}

// ApprovalSigningBytes returns the canonical bytes a contact signs. It covers
// the account, recovery request identity, all request-bound fields, the
// contactID, and the contact key fingerprint.
func ApprovalSigningBytes(accountID AccountID, recoveryID RecoveryID, epoch uint64, target, device Digest, deadline int64, contactID ContactID, fingerprint Fingerprint) []byte {
	return approvalSigningBytesForFields(accountID, recoveryID, epoch, target, device, deadline, contactID, fingerprint)
}

func approvalSigningBytes(approval Approval) []byte {
	return approvalSigningBytesForFields(
		approval.AccountID,
		approval.RecoveryID,
		approval.Epoch,
		approval.TargetDigest,
		approval.DeviceDigest,
		approval.Deadline.UnixNano(),
		approval.ContactID,
		approval.ContactFingerprint,
	)
}

// RecoveryApprovalMessage returns the canonical message for an approval of req
// by contact.
func RecoveryApprovalMessage(req *Recovery, contact Contact) []byte {
	return approvalSigningBytesForFields(
		req.AccountID,
		req.ID,
		req.Epoch,
		req.TargetDigest,
		req.DeviceDigest,
		req.Deadline.UnixNano(),
		contact.ContactID,
		contact.Fingerprint,
	)
}

// SignApproval signs a recovery request for one contact.
func SignApproval(privateKey ed25519.PrivateKey, req *Recovery, contact Contact) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: invalid ed25519 private key", ErrInvalidSignature)
	}
	if contact.Fingerprint.IsZero() {
		contact.Fingerprint = PublicKeyFingerprint(contact.PublicKey)
	}
	return ed25519.Sign(privateKey, RecoveryApprovalMessage(req, contact)), nil
}

func vetoSigningBytes(accountID AccountID, recoveryID RecoveryID, epoch uint64, target, device Digest, deadline int64, factorID StrongFactorID) []byte {
	var buf bytes.Buffer
	buf.Write(vetoSignatureDomain)
	appendString(&buf, string(accountID))
	appendString(&buf, string(recoveryID))
	appendUint64(&buf, epoch)
	appendBytes(&buf, target[:])
	appendBytes(&buf, device[:])
	appendUint64(&buf, uint64(deadline))
	appendString(&buf, string(factorID))
	return buf.Bytes()
}

// VetoSigningBytes returns the canonical bytes a strong factor signs to veto a
// recovery request.
func VetoSigningBytes(req *Recovery, factorID StrongFactorID) []byte {
	return vetoSigningBytes(req.AccountID, req.ID, req.Epoch, req.TargetDigest, req.DeviceDigest, req.Deadline.UnixNano(), factorID)
}

// SignVeto signs a veto assertion for a recovery request.
func SignVeto(privateKey ed25519.PrivateKey, req *Recovery, factorID StrongFactorID) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: invalid ed25519 private key", ErrInvalidSignature)
	}
	return ed25519.Sign(privateKey, VetoSigningBytes(req, factorID)), nil
}

func randomBytes(source io.Reader, size int) ([]byte, error) {
	if source == nil {
		source = rand.Reader
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(source, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func randomHex(source io.Reader, size int) (string, error) {
	raw, err := randomBytes(source, size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func newRecoveryID(source io.Reader) (RecoveryID, error) {
	id, err := randomHex(source, recoveryIDBytes)
	if err != nil {
		return "", err
	}
	return RecoveryID(id), nil
}

func verifyApprovalSignature(contact Contact, approval Approval) bool {
	if len(contact.PublicKey) != ed25519.PublicKeySize || len(approval.Signature) != ed25519.SignatureSize {
		return false
	}
	if contact.Fingerprint.IsZero() {
		contact.Fingerprint = PublicKeyFingerprint(contact.PublicKey)
	}
	if subtle.ConstantTimeCompare(contact.Fingerprint[:], approval.ContactFingerprint[:]) != 1 {
		return false
	}
	return ed25519.Verify(contact.PublicKey, approvalSigningBytes(approval), approval.Signature)
}

func verifyVetoSignature(factor StrongFactor, signature []byte, req *Recovery) bool {
	if len(factor.PublicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(factor.PublicKey, VetoSigningBytes(req, factor.ID), signature)
}
