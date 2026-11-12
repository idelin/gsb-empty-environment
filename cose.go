package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
)

// COSE header parameter labels (RFC 9052) plus private-use labels for the
// tenant and message-type bindings required by the device gateway.
const (
	LabelAlgorithm int64 = 1
	LabelKeyID     int64 = 4
	LabelTenant    int64 = -70001
	LabelMsgType   int64 = -70002
)

// AlgEdDSA is the only supported COSE algorithm (Ed25519, PureEdDSA).
const AlgEdDSA int64 = -8

// coseSign1Tag is the CBOR tag optionally wrapping a COSE_Sign1 structure.
const coseSign1Tag uint64 = 18

var (
	ErrMalformedMessage     = errors.New("cose: malformed message")
	ErrDuplicateLabel       = errors.New("cose: header label repeated or present in both protected and unprotected buckets")
	ErrUnsupportedAlgorithm = errors.New("cose: algorithm missing or not EdDSA")
	ErrTenantMismatch       = errors.New("cose: tenant header missing or does not match the expected context")
	ErrMsgTypeMismatch      = errors.New("cose: message-type header missing or does not match the expected context")
	ErrSignatureMismatch    = errors.New("cose: signature verification failed")
)

// Context is the caller-supplied security context. Tenant and MsgType are
// matched against the message headers; Tenant, Version and Purpose are bound
// into the signature via external_aad.
type Context struct {
	Tenant  string
	MsgType string
	Version string
	Purpose string
}

// ExternalAAD builds the external_aad input to Sig_structure, binding the
// tenant, protocol version and purpose to the signature.
func (c Context) ExternalAAD() []byte {
	var e cborEncoder
	e.array(3)
	e.tstr(c.Tenant)
	e.tstr(c.Version)
	e.tstr(c.Purpose)
	return e.buf
}

// Message is a successfully verified COSE_Sign1 message. Payload is only
// exposed after the signature has been verified.
type Message struct {
	Tenant  string
	MsgType string
	KeyID   []byte
	Payload []byte
}

// sign1Envelope is the raw, not-yet-trusted decomposition of a COSE_Sign1.
type sign1Envelope struct {
	protectedRaw   []byte // exact content bytes of the protected bstr
	unprotectedRaw []byte // raw encoded unprotected map
	payload        []byte // content bytes of the payload bstr
	signature      []byte // content bytes of the signature bstr
}

// parseSign1 decomposes a COSE_Sign1 without interpreting or re-encoding
// anything, so the protected header bytes stay exactly as signed.
func parseSign1(data []byte) (*sign1Envelope, error) {
	d := &cborDecoder{data: data}
	major, arg, err := d.readHead()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if major == majorTag {
		if arg != coseSign1Tag {
			return nil, fmt.Errorf("%w: unexpected CBOR tag %d", ErrMalformedMessage, arg)
		}
		if major, arg, err = d.readHead(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
		}
	}
	if major != majorArray || arg != 4 {
		return nil, fmt.Errorf("%w: COSE_Sign1 must be an array of 4 items", ErrMalformedMessage)
	}
	env := &sign1Envelope{}
	if env.protectedRaw, err = d.readBstr(); err != nil {
		return nil, fmt.Errorf("%w: protected headers: %v", ErrMalformedMessage, err)
	}
	if env.unprotectedRaw, err = d.skip(0); err != nil {
		return nil, fmt.Errorf("%w: unprotected headers: %v", ErrMalformedMessage, err)
	}
	if env.payload, err = d.readBstr(); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedMessage, err)
	}
	if env.signature, err = d.readBstr(); err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrMalformedMessage, err)
	}
	if d.off != len(d.data) {
		return nil, fmt.Errorf("%w: trailing data after COSE_Sign1", ErrMalformedMessage)
	}
	return env, nil
}

// headerLabel is a decoded CBOR map label; comparison is by value, so
// non-canonical encodings of the same label still collide.
type headerLabel struct {
	isText bool
	num    int64
	text   string
}

// headerSet holds the parameters decoded from one header bucket.
type headerSet struct {
	alg        int64
	hasAlg     bool
	kid        []byte
	hasKid     bool
	tenant     string
	hasTenant  bool
	msgType    string
	hasMsgType bool
	labels     map[headerLabel]struct{}
}

func decodeLabel(raw []byte) (headerLabel, error) {
	d := &cborDecoder{data: raw}
	if d.off >= len(raw) {
		return headerLabel{}, errCBOREOF
	}
	switch raw[0] >> 5 {
	case majorUint, majorNint:
		n, err := d.readInt()
		if err != nil {
			return headerLabel{}, err
		}
		if d.off != len(raw) {
			return headerLabel{}, errCBORType
		}
		return headerLabel{num: n}, nil
	case majorTstr:
		s, err := d.readTstr()
		if err != nil {
			return headerLabel{}, err
		}
		if d.off != len(raw) {
			return headerLabel{}, errCBORType
		}
		return headerLabel{isText: true, text: s}, nil
	}
	return headerLabel{}, errCBORType
}

// decodeHeaderMap decodes one header bucket. An empty byte string denotes
// the empty map (only valid for the protected bucket encoding).
func decodeHeaderMap(raw []byte) (*headerSet, error) {
	hs := &headerSet{labels: make(map[headerLabel]struct{})}
	if len(raw) == 0 {
		return hs, nil
	}
	d := &cborDecoder{data: raw}
	major, arg, err := d.readHead()
	if err != nil {
		return nil, fmt.Errorf("%w: header map: %v", ErrMalformedMessage, err)
	}
	if major != majorMap {
		return nil, fmt.Errorf("%w: headers must be a CBOR map", ErrMalformedMessage)
	}
	if arg > uint64(len(raw))/2 {
		return nil, fmt.Errorf("%w: header map truncated", ErrMalformedMessage)
	}
	for i := uint64(0); i < arg; i++ {
		rawLabel, err := d.skip(0)
		if err != nil {
			return nil, fmt.Errorf("%w: header label: %v", ErrMalformedMessage, err)
		}
		label, err := decodeLabel(rawLabel)
		if err != nil {
			return nil, fmt.Errorf("%w: header label: %v", ErrMalformedMessage, err)
		}
		if _, dup := hs.labels[label]; dup {
			return nil, fmt.Errorf("%w: label %v", ErrDuplicateLabel, label)
		}
		hs.labels[label] = struct{}{}
		switch {
		case !label.isText && label.num == LabelAlgorithm:
			v, err := d.readInt()
			if err != nil {
				return nil, fmt.Errorf("%w: alg value: %v", ErrMalformedMessage, err)
			}
			hs.alg, hs.hasAlg = v, true
		case !label.isText && label.num == LabelKeyID:
			v, err := d.readBstr()
			if err != nil {
				return nil, fmt.Errorf("%w: kid value: %v", ErrMalformedMessage, err)
			}
			hs.kid, hs.hasKid = v, true
		case !label.isText && label.num == LabelTenant:
			v, err := d.readTstr()
			if err != nil {
				return nil, fmt.Errorf("%w: tenant value: %v", ErrMalformedMessage, err)
			}
			hs.tenant, hs.hasTenant = v, true
		case !label.isText && label.num == LabelMsgType:
			v, err := d.readTstr()
			if err != nil {
				return nil, fmt.Errorf("%w: message-type value: %v", ErrMalformedMessage, err)
			}
			hs.msgType, hs.hasMsgType = v, true
		default:
			if _, err := d.skip(0); err != nil {
				return nil, fmt.Errorf("%w: header value: %v", ErrMalformedMessage, err)
			}
		}
	}
	if d.off != len(raw) {
		return nil, fmt.Errorf("%w: trailing data in header map", ErrMalformedMessage)
	}
	return hs, nil
}

// mergeHeaders combines the two buckets, rejecting any label that appears in
// both (this covers alg, kid, tenant and message type as required).
func mergeHeaders(protected, unprotected *headerSet) (*headerSet, error) {
	for label := range protected.labels {
		if _, dup := unprotected.labels[label]; dup {
			return nil, fmt.Errorf("%w: label %v in both buckets", ErrDuplicateLabel, label)
		}
	}
	merged := &headerSet{}
	for _, hs := range []*headerSet{protected, unprotected} {
		if hs.hasAlg {
			merged.alg, merged.hasAlg = hs.alg, true
		}
		if hs.hasKid {
			merged.kid, merged.hasKid = hs.kid, true
		}
		if hs.hasTenant {
			merged.tenant, merged.hasTenant = hs.tenant, true
		}
		if hs.hasMsgType {
			merged.msgType, merged.hasMsgType = hs.msgType, true
		}
	}
	return merged, nil
}

// sigStructure builds the Sig_structure to be verified (RFC 9052 4.4).
// bodyProtected is the exact byte content of the protected bstr.
func sigStructure(bodyProtected, externalAAD, payload []byte) []byte {
	var e cborEncoder
	e.array(4)
	e.tstr("Signature1")
	e.bstr(bodyProtected)
	e.bstr(externalAAD)
	e.bstr(payload)
	return e.buf
}

// VerifySign1 verifies a COSE_Sign1 message against the Ed25519 public key
// and the caller-supplied context. The payload is decoded and returned only
// after the signature has been verified.
func VerifySign1(key ed25519.PublicKey, message []byte, ctx Context) (*Message, error) {
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("cose: invalid Ed25519 public key size %d", len(key))
	}
	env, err := parseSign1(message)
	if err != nil {
		return nil, err
	}
	protected, err := decodeHeaderMap(env.protectedRaw)
	if err != nil {
		return nil, err
	}
	unprotected, err := decodeHeaderMap(env.unprotectedRaw)
	if err != nil {
		return nil, err
	}
	hdrs, err := mergeHeaders(protected, unprotected)
	if err != nil {
		return nil, err
	}
	if !hdrs.hasAlg || hdrs.alg != AlgEdDSA {
		return nil, ErrUnsupportedAlgorithm
	}
	if !hdrs.hasTenant || hdrs.tenant != ctx.Tenant {
		return nil, ErrTenantMismatch
	}
	if !hdrs.hasMsgType || hdrs.msgType != ctx.MsgType {
		return nil, ErrMsgTypeMismatch
	}
	toVerify := sigStructure(env.protectedRaw, ctx.ExternalAAD(), env.payload)
	if !ed25519.Verify(key, toVerify, env.signature) {
		return nil, ErrSignatureMismatch
	}
	return &Message{
		Tenant:  hdrs.tenant,
		MsgType: hdrs.msgType,
		KeyID:   hdrs.kid,
		Payload: env.payload,
	}, nil
}
