package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// SignRequest is the object handed to the key host for signing. It carries no
// bare alias identity: every alias-derived field is fully qualified by
// account and region and is pinned to the binding versions and final public
// key fingerprint produced by one resolution transaction.
type SignRequest struct {
	RequestID        string      `json:"request_id"`
	CallerID         string      `json:"caller_id"`
	Entry            AliasRef    `json:"entry"`
	EntryVersion     uint64      `json:"entry_version"`
	FinalAccount     AccountID   `json:"final_account"`
	FinalRegion      Region      `json:"final_region"`
	FinalKey         KeyID       `json:"final_key"`
	FinalFingerprint Fingerprint `json:"final_fingerprint"`
	Hops             []Hop       `json:"hops"`
	Payload          []byte      `json:"payload"`
	IssuedAt         time.Time   `json:"issued_at"`
	KeyDER           []byte      `json:"key_der,omitempty"`
}

// SignRequestOption customizes BuildSignRequest.
type SignRequestOption func(*SignRequest)

// WithRequestID sets an explicit request identifier.
func WithRequestID(id string) SignRequestOption {
	return func(req *SignRequest) { req.RequestID = id }
}

// WithPayload attaches the bytes the host is asked to sign.
func WithPayload(payload []byte) SignRequestOption {
	return func(req *SignRequest) { req.Payload = append([]byte(nil), payload...) }
}

// WithIssuedAt sets an explicit issuance timestamp.
func WithIssuedAt(at time.Time) SignRequestOption {
	return func(req *SignRequest) { req.IssuedAt = at.UTC() }
}

// WithKeyDER attaches the resolved public key DER so the host can recompute
// the fingerprint before signing.
func WithKeyDER(der []byte) SignRequestOption {
	return func(req *SignRequest) { req.KeyDER = append([]byte(nil), der...) }
}

// BuildSignRequest constructs a tamper-evident signature request from a
// pinned resolution. The request can only be signed by the key matching
// FinalFingerprint; an alias name by itself never authorizes anything.
func BuildSignRequest(caller CallerIdentity, res Resolution, options ...SignRequestOption) (SignRequest, error) {
	if res.FinalFingerprint == "" {
		return SignRequest{}, ErrInvalidSignRequest
	}
	req := SignRequest{
		RequestID:        "sign-" + hex.EncodeToString(randomRequestIDSuffix()),
		CallerID:         caller.ID,
		Entry:            res.Entry,
		EntryVersion:     res.EntryVersion,
		FinalAccount:     res.FinalAccount,
		FinalRegion:      res.FinalRegion,
		FinalKey:         res.FinalKey,
		FinalFingerprint: res.FinalFingerprint,
		Hops:             append([]Hop(nil), res.Hops...),
		IssuedAt:         time.Now().UTC(),
	}
	for _, option := range options {
		option(&req)
	}
	if err := req.Verify(); err != nil {
		return SignRequest{}, err
	}
	return req, nil
}

// Canonical returns the deterministic byte encoding of the request. All
// consumers digest and sign exactly these bytes.
func (req SignRequest) Canonical() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(req); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Digest returns the SHA-256 digest of the canonical encoding.
func (req SignRequest) Digest() ([32]byte, error) {
	canonical, err := req.Canonical()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

// Verify checks structural completeness and internal consistency. Any
// mismatch — including an alias whose final key no longer matches the pinned
// fingerprint — makes the request invalid.
func (req SignRequest) Verify() error {
	switch {
	case req.RequestID == "":
		return ErrInvalidSignRequest
	case !req.Entry.Valid():
		return ErrInvalidSignRequest
	case req.FinalAccount == "" || req.FinalKey == "" || req.FinalFingerprint == "":
		return ErrInvalidSignRequest
	case req.IssuedAt.IsZero():
		return ErrInvalidSignRequest
	case len(req.Hops) == 0:
		return ErrInvalidSignRequest
	}

	seen := make(map[AliasRef]struct{}, len(req.Hops))
	for _, hop := range req.Hops {
		if !hop.Ref.Valid() || hop.Version == 0 {
			return ErrInvalidSignRequest
		}
		if _, dup := seen[hop.Ref]; dup {
			return ErrCycle
		}
		seen[hop.Ref] = struct{}{}
	}

	if req.Hops[0].Ref != req.Entry || req.Hops[0].Version != req.EntryVersion {
		return ErrInvalidSignRequest
	}
	last := req.Hops[len(req.Hops)-1]
	if last.Target.Kind != TargetKey || last.Target.Key != req.FinalKey {
		return ErrInvalidSignRequest
	}
	if len(req.KeyDER) > 0 && SHA256Fingerprint(req.KeyDER) != req.FinalFingerprint {
		return ErrFingerprintMismatch
	}
	return nil
}
