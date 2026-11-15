package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
)

var ErrKeyUnavailable = errors.New("EVM verification key is not loaded")

// Verdict is fail-closed: its zero value denies access.
type Verdict uint8

const (
	VerdictDeny Verdict = iota
	VerdictAllow
	DecisionDeny  = VerdictDeny
	DecisionAllow = VerdictAllow
	Deny          = VerdictDeny
	Allow         = VerdictAllow
)

func (v Verdict) String() string {
	if v == VerdictAllow {
		return "allow"
	}
	return "deny"
}

// Reason identifies why an evaluation was allowed or denied.
type Reason string

const (
	ReasonNone              Reason = ""
	ReasonAllowed           Reason = "allowed"
	ReasonKeyUnavailable    Reason = "key-unavailable"
	ReasonMissingEVM        Reason = "evm-missing"
	ReasonMalformedEVM      Reason = "evm-malformed"
	ReasonUnknownType       Reason = "evm-unknown-type"
	ReasonHMACMismatch      Reason = "evm-hmac-mismatch"
	ReasonAttributeMismatch Reason = "evm-attribute-mismatch"
	ReasonInodeMismatch     Reason = "evm-inode-mismatch"
	ReasonBindingMismatch   Reason = "evm-binding-mismatch"
	ReasonSignatureInvalid  Reason = "evm-signature-invalid"
	ReasonUnsupportedKey    Reason = "evm-unsupported-key"
	ReasonUnsupported       Reason = "unsupported"
	ReasonIOError           Reason = "io-error"
	ReasonInvalidState      Reason = "invalid-state"
)

// Result is the outcome of an evaluation. Access must only proceed when both
// Verdict is VerdictAllow and Allowed returns true.
type Result struct {
	Verdict Verdict
	Reason  Reason
	Type    byte
	Detail  string
}

func (r Result) Allowed() bool {
	return r.Verdict == VerdictAllow
}

func (r Result) OK() bool {
	return r.Allowed()
}

func (r Result) String() string {
	if r.Detail == "" {
		return fmt.Sprintf("%s: %s", r.Reason, r.Verdict)
	}
	return fmt.Sprintf("%s: %s: %s", r.Reason, r.Verdict, r.Detail)
}

// Evaluator verifies security.evm with a local HMAC key and/or trusted public
// keys. It is immutable after construction and safe for concurrent use.
type Evaluator struct {
	hostKey     []byte
	trustedKeys []crypto.PublicKey
}

func NewEvaluator(hostKey []byte, trustedKeys ...crypto.PublicKey) *Evaluator {
	return &Evaluator{
		hostKey:     bytes.Clone(hostKey),
		trustedKeys: cloneTrustedKeys(trustedKeys),
	}
}

func New(hostKey []byte, trustedKeys ...crypto.PublicKey) *Evaluator {
	return NewEvaluator(hostKey, trustedKeys...)
}

func (e *Evaluator) WithHostKey(hostKey []byte) *Evaluator {
	next := &Evaluator{trustedKeys: cloneTrustedKeys(e.trustedKeys)}
	next.hostKey = bytes.Clone(hostKey)
	return next
}

func (e *Evaluator) WithTrustedKeys(trustedKeys ...crypto.PublicKey) *Evaluator {
	next := &Evaluator{
		hostKey:     bytes.Clone(e.hostKey),
		trustedKeys: cloneTrustedKeys(trustedKeys),
	}
	return next
}

// Evaluate decides whether a file snapshot may be accessed. A missing key is
// always a denial; there is no IMA-only fallback.
func (e *Evaluator) Evaluate(state FileState) Result {
	if e == nil || (len(e.hostKey) == 0 && len(e.trustedKeys) == 0) {
		return denied(ReasonKeyUnavailable, 0, ErrKeyUnavailable.Error())
	}

	record, err := ParseEVM(state.EVM)
	if err != nil {
		return resultFromError(err, 0)
	}

	switch record.Type {
	case EVMTypeHMAC:
		return e.evaluateHMAC(state, record)
	case EVMTypePortableSignature:
		return e.evaluatePortable(state, record)
	default:
		return denied(ReasonUnknownType, record.Type,
			fmt.Sprintf("unsupported security.evm type %d", record.Type))
	}
}

func (e *Evaluator) evaluateHMAC(state FileState, record EVMRecord) Result {
	if len(e.hostKey) == 0 {
		return denied(ReasonKeyUnavailable, record.Type, ErrKeyUnavailable.Error())
	}

	var expected []byte
	switch len(record.Payload) {
	case sha1.Size:
		expected = ComputeHMACSHA1(state, e.hostKey)
	case sha256.Size:
		expected = ComputeHMAC(state, e.hostKey)
	default:
		return denied(ReasonMalformedEVM, record.Type,
			fmt.Sprintf("HMAC length is %d, want %d or %d",
				len(record.Payload), sha1.Size, sha256.Size))
	}

	if subtle.ConstantTimeCompare(expected, record.Payload) != 1 {
		return denied(ReasonHMACMismatch, record.Type,
			"protected attributes, inode binding, or inode generation changed")
	}
	return allowed(record.Type)
}

func (e *Evaluator) evaluatePortable(state FileState, record EVMRecord) Result {
	if len(e.trustedKeys) == 0 {
		return denied(ReasonKeyUnavailable, record.Type, ErrKeyUnavailable.Error())
	}
	if len(record.Payload) == 0 {
		return denied(ReasonMalformedEVM, record.Type, "portable signature is empty")
	}

	valid, supported := verifySignature(PortableSignatureInput(state), record.Payload, e.trustedKeys)
	if valid {
		return allowed(record.Type)
	}
	if !supported {
		return denied(ReasonUnsupportedKey, record.Type,
			"no trusted key supports the signature encoding")
	}
	return denied(ReasonSignatureInvalid, record.Type,
		"portable signature does not authenticate the protected attributes")
}

// EvaluateTransition classifies a failure using a previously trusted snapshot.
// It still performs a fresh cryptographic check of current. The previous state
// is used only to distinguish an attribute change from a changed inode
// binding; a path difference with the same inode is a valid hard link.
func (e *Evaluator) EvaluateTransition(previous, current FileState) Result {
	fresh := e.Evaluate(current)
	if fresh.Allowed() {
		return fresh
	}
	if fresh.Reason != ReasonHMACMismatch && fresh.Reason != ReasonSignatureInvalid {
		return fresh
	}

	identityChanged := previous.Inode != current.Inode ||
		previous.Generation != current.Generation
	attributesChanged := !sameProtectedAttributes(previous, current)

	switch {
	case identityChanged && attributesChanged:
		return denied(ReasonBindingMismatch, fresh.Type,
			"both protected attributes and inode identity changed")
	case identityChanged && fresh.Type == EVMTypeHMAC:
		return denied(ReasonInodeMismatch, fresh.Type,
			"HMAC was issued for a different inode or inode generation")
	case attributesChanged:
		return denied(ReasonAttributeMismatch, fresh.Type,
			"security.ima, security.selinux, uid, gid, or mode changed")
	default:
		return fresh
	}
}

func sameProtectedAttributes(a, b FileState) bool {
	return bytes.Equal(a.IMA, b.IMA) &&
		bytes.Equal(a.SELinux, b.SELinux) &&
		a.UID == b.UID &&
		a.GID == b.GID &&
		a.Mode == b.Mode
}

func allowed(typ byte) Result {
	return Result{
		Verdict: VerdictAllow,
		Reason:  ReasonAllowed,
		Type:    typ,
		Detail:  recordTypeName(typ) + " verified",
	}
}

func denied(reason Reason, typ byte, detail string) Result {
	return Result{
		Verdict: VerdictDeny,
		Reason:  reason,
		Type:    typ,
		Detail:  detail,
	}
}

func resultFromError(err error, typ byte) Result {
	switch {
	case errors.Is(err, ErrMissingEVM):
		return denied(ReasonMissingEVM, typ, err.Error())
	case errors.Is(err, ErrUnknownEVMType):
		return denied(ReasonUnknownType, typ, err.Error())
	default:
		return denied(ReasonMalformedEVM, typ, err.Error())
	}
}

func Evaluate(state FileState, hostKey []byte, trustedKeys ...crypto.PublicKey) Result {
	return NewEvaluator(hostKey, trustedKeys...).Evaluate(state)
}

func EvaluateState(state FileState, hostKey []byte, trustedKeys ...crypto.PublicKey) Result {
	return Evaluate(state, hostKey, trustedKeys...)
}

func EvaluateTransition(previous, current FileState, hostKey []byte, trustedKeys ...crypto.PublicKey) Result {
	return NewEvaluator(hostKey, trustedKeys...).EvaluateTransition(previous, current)
}

func EvaluatePath(path string, hostKey []byte, trustedKeys ...crypto.PublicKey) Result {
	return NewEvaluator(hostKey, trustedKeys...).EvaluatePath(path)
}
