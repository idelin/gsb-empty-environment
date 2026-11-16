package netsec

import (
	"errors"
	"fmt"
)

const (
	// HostKeysRequest is the OpenSSH global request that advertises the
	// server's complete set of host keys.
	HostKeysRequest = "hostkeys-00@openssh.com"
	// HostKeysProveRequest is the OpenSSH global request used to ask the
	// server to sign a proof for one or more advertised host keys.
	HostKeysProveRequest = "hostkeys-prove-00@openssh.com"
)

var (
	// ErrHostNotAuthenticated refuses a notification before the SSH transport
	// has authenticated the server host.
	ErrHostNotAuthenticated = errors.New("server host has not been authenticated")
	// ErrNoKnownHostsEntry refuses a notification unless known_hosts already
	// contains an entry for this host.
	ErrNoKnownHostsEntry = errors.New("host has no known_hosts entry")
	// ErrNoProof indicates that no usable proof was supplied.
	ErrNoProof = errors.New("host key proof is missing")
	// ErrProofVerification indicates that a host key proof did not verify.
	ErrProofVerification = errors.New("host key proof did not verify")
	// ErrProofCount indicates that the proof response did not match the
	// number of keys requested.
	ErrProofCount = errors.New("host key proof response has the wrong number of signatures")
	// ErrRevokedKey refuses an update that contains a key revoked for this
	// host in known_hosts.
	ErrRevokedKey = errors.New("server advertised a revoked host key")
	// ErrInvalidNotification indicates that the hostkeys notification was
	// malformed.
	ErrInvalidNotification = errors.New("invalid hostkeys notification")
)

// Request describes one hostkeys-00@openssh.com notification. Host must be
// the known_hosts host name (for example "example.com" or
// "[example.com]:2222"), and Payload is the global request payload.
//
// HostAuthenticated must only be set after the SSH client has successfully
// verified the server's host key. This package does not infer authentication
// from the presence of a global request.
type Request struct {
	Host              string
	KnownHostsPath    string
	SessionID         []byte
	HostAuthenticated bool
	Payload           []byte
}

// Notification is an alias for Request.
type Notification = Request

// Result reports the verified changes that were committed to known_hosts.
type Result struct {
	Added   []HostKey
	Removed []HostKey
	Changed bool
}

// Prover obtains signatures for hostkeys-prove-00@openssh.com. Keys and
// returned signatures are complete SSH wire-encoded blobs in the same order.
// Implementations should send a single global request with wantReply=true.
type Prover interface {
	ProveHostKeys(keys [][]byte) ([][]byte, error)
}

// ProverFunc adapts a function to Prover.
type ProverFunc func(keys [][]byte) ([][]byte, error)

// ProveHostKeys implements Prover.
func (function ProverFunc) ProveHostKeys(keys [][]byte) ([][]byte, error) {
	if function == nil {
		return nil, ErrNoProof
	}
	return function(keys)
}

// SingleProverFunc adapts a per-key proof function to Prover.
type SingleProverFunc func(key []byte) ([]byte, error)

// ProveHostKeys implements Prover.
func (function SingleProverFunc) ProveHostKeys(keys [][]byte) ([][]byte, error) {
	if function == nil {
		return nil, ErrNoProof
	}
	signatures := make([][]byte, len(keys))
	for index, key := range keys {
		signature, err := function(key)
		if err != nil {
			return nil, fmt.Errorf("prove key %d: %w", index, err)
		}
		signatures[index] = signature
	}
	return signatures, nil
}

// RequestSender is the subset of ssh.Conn used to make a proof request.
// *ssh.Client and ssh.Conn values from golang.org/x/crypto/ssh satisfy it.
type RequestSender interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// ConnProver obtains proofs through an existing SSH connection.
type ConnProver struct {
	Conn RequestSender
}

// ProveHostKeys implements Prover.
func (prover ConnProver) ProveHostKeys(keys [][]byte) ([][]byte, error) {
	if prover.Conn == nil {
		return nil, ErrNoProof
	}
	ok, response, err := prover.Conn.SendRequest(HostKeysProveRequest, true, marshalStringList(keys))
	if err != nil {
		return nil, fmt.Errorf("send %s: %w", HostKeysProveRequest, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: server rejected %s", ErrProofVerification, HostKeysProveRequest)
	}
	signatures, err := parseStringList(response)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProofVerification, err)
	}
	return signatures, nil
}

// ProofError identifies the key whose proof failed.
type ProofError struct {
	Key HostKey
	Err error
}

// Error implements error.
func (err *ProofError) Error() string {
	return fmt.Sprintf("host key %s proof: %v", err.Key.Fingerprint(), err.Err)
}

// Unwrap implements errors.Unwrap.
func (err *ProofError) Unwrap() error {
	return err.Err
}

// ParseHostKeys parses the payload of a hostkeys-00@openssh.com request.
func ParseHostKeys(payload []byte) ([]HostKey, error) {
	blobs, err := parseStringList(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNotification, err)
	}

	keys := make([]HostKey, 0, len(blobs))
	seen := make(map[string]struct{}, len(blobs))
	for _, blob := range blobs {
		key, err := ParseHostKey(blob)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidNotification, err)
		}
		if _, exists := seen[string(key.Blob)]; exists {
			return nil, fmt.Errorf("%w: duplicate host key %s", ErrInvalidNotification, key.Fingerprint())
		}
		seen[string(key.Blob)] = struct{}{}
		key.Blob = append([]byte(nil), key.Blob...)
		keys = append(keys, key)
	}
	return keys, nil
}

// ProofData returns the bytes signed by hostkeys-prove-00@openssh.com:
//
//	string HostKeysProveRequest
//	string session identifier
//	string public key blob
func ProofData(sessionID, keyBlob []byte) []byte {
	data := appendString(nil, []byte(HostKeysProveRequest))
	data = appendString(data, sessionID)
	data = appendString(data, keyBlob)
	return data
}

// VerifyProof verifies an SSH signature blob for a host key proof.
func VerifyProof(key HostKey, sessionID, signature []byte) error {
	if len(sessionID) == 0 {
		return fmt.Errorf("%w: empty session identifier", ErrProofVerification)
	}
	if err := key.Verify(ProofData(sessionID, key.Blob), signature); err != nil {
		return fmt.Errorf("%w: %v", ErrProofVerification, err)
	}
	return nil
}

// Handle validates a hostkeys notification, proves every key that is required
// for the proposed change, and atomically commits the complete update. No
// known_hosts write occurs if any proof fails.
func Handle(request Request, prover Prover) (Result, error) {
	if !request.HostAuthenticated {
		return Result{}, ErrHostNotAuthenticated
	}
	if len(request.SessionID) == 0 {
		return Result{}, fmt.Errorf("%w: empty session identifier", ErrNoProof)
	}
	if err := validateKnownHostsHost(request.Host); err != nil {
		return Result{}, err
	}
	if request.KnownHostsPath == "" {
		return Result{}, errors.New("known_hosts path is empty")
	}

	advertised, err := ParseHostKeys(request.Payload)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := loadKnownHosts(request.KnownHostsPath, request.Host)
	if err != nil {
		return Result{}, err
	}
	if !snapshot.hasEntry {
		return Result{}, ErrNoKnownHostsEntry
	}

	advertisedSet := make(map[string]HostKey, len(advertised))
	for _, key := range advertised {
		advertisedSet[string(key.Blob)] = key
		if snapshot.isRevoked(key.Blob) {
			return Result{}, fmt.Errorf("%w: %s", ErrRevokedKey, key.Fingerprint())
		}
	}

	existingSet := make(map[string]HostKey, len(snapshot.keys))
	for _, key := range snapshot.keys {
		existingSet[string(key.Blob)] = key
	}

	var additions []HostKey
	for _, key := range advertised {
		if _, exists := existingSet[string(key.Blob)]; !exists {
			additions = append(additions, key)
		}
	}
	var removals []HostKey
	for _, key := range snapshot.keys {
		if _, stillAdvertised := advertisedSet[string(key.Blob)]; !stillAdvertised {
			removals = append(removals, key)
		}
	}
	if len(additions) == 0 && len(removals) == 0 {
		return Result{}, nil
	}

	proofKeys := additions
	if len(removals) > 0 && len(proofKeys) == 0 {
		// A removal cannot be proven directly because the key is absent from
		// the advertised set. Prove every retained key instead, so the full
		// advertised set is authenticated before deleting anything.
		proofKeys = advertised
	}
	if len(proofKeys) == 0 {
		return Result{}, ErrNoProof
	}
	if prover == nil {
		return Result{}, ErrNoProof
	}

	signatures, err := prover.ProveHostKeys(hostKeyBlobs(proofKeys))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrProofVerification, err)
	}
	if len(signatures) != len(proofKeys) {
		return Result{}, fmt.Errorf("%w: requested %d, received %d", ErrProofCount, len(proofKeys), len(signatures))
	}
	for index, key := range proofKeys {
		if len(signatures[index]) == 0 {
			return Result{}, &ProofError{Key: key, Err: ErrNoProof}
		}
		if err := VerifyProof(key, request.SessionID, signatures[index]); err != nil {
			return Result{}, &ProofError{Key: key, Err: err}
		}
	}

	actualRemoved, changed, err := snapshot.commit(additions, removals)
	if err != nil {
		return Result{}, err
	}
	if !changed {
		return Result{}, nil
	}
	return Result{
		Added:   cloneHostKeys(additions),
		Removed: cloneHostKeys(actualRemoved),
		Changed: true,
	}, nil
}

// HandleNotification is an alias for Handle.
func HandleNotification(notification Notification, prover Prover) (Result, error) {
	return Handle(notification, prover)
}

func hostKeyBlobs(keys []HostKey) [][]byte {
	blobs := make([][]byte, len(keys))
	for index := range keys {
		blobs[index] = keys[index].Blob
	}
	return blobs
}

func cloneHostKeys(keys []HostKey) []HostKey {
	cloned := make([]HostKey, len(keys))
	for index, key := range keys {
		cloned[index] = HostKey{Type: key.Type, Blob: append([]byte(nil), key.Blob...)}
	}
	return cloned
}
