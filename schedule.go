package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

type keyArgs struct {
	key    []byte
	typ    PSKType
	typSet bool
	hash   HashFunc
}

func parseKeyArgs(args []any) (keyArgs, error) {
	out := keyArgs{hash: sha256.New}
	foundKey := false

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case []byte:
			if !foundKey {
				out.key = v
				foundKey = true
			}
		case PSK:
			out.key = v.Key
			out.typ = v.Type
			out.typSet = true
			foundKey = true
		case *PSK:
			if v != nil {
				out.key = v.Key
				out.typ = v.Type
				out.typSet = true
				foundKey = true
			}
		case PSKType:
			out.typ = v
			out.typSet = true
		case bool:
			if v {
				out.typ = PSKExternal
			} else {
				out.typ = PSKResumption
			}
			out.typSet = true
		default:
			if b, ok := byteSliceValue(v); ok {
				if !foundKey {
					out.key = b
					foundKey = true
				}
				continue
			}
			if t, ok := resolvePSKType(v); ok {
				out.typ = t
				out.typSet = true
				continue
			}
			if h, ok := resolveHash(v); ok {
				out.hash = h
				continue
			}
			if s, ok := v.(string); ok {
				if !foundKey {
					out.key = []byte(s)
					foundKey = true
				}
				continue
			}
			return out, fmt.Errorf("netsec: unsupported key schedule argument %T", arg)
		}
	}
	if !foundKey {
		return out, errors.New("netsec: missing PSK")
	}
	return out, nil
}

// EarlySecret is the TLS 1.3 early secret and its PSK binder key schedule.
// Keeping the type explicit here prevents an external PSK from silently using
// the resumption binder label (or vice versa).
type EarlySecret struct {
	secret []byte
	hash   HashFunc
	typ    PSKType
	typSet bool
}

// KeySchedule is an alias for EarlySecret.
type KeySchedule = EarlySecret

// TLS13KeySchedule is an alias for EarlySecret.
type TLS13KeySchedule = EarlySecret

// NewEarlySecret computes HKDF-Extract(0, psk). The hash constructor and PSK
// may be supplied in either order; an optional PSK type may also be supplied.
// If no hash is supplied, SHA-256 is used.
func NewEarlySecret(args ...any) *EarlySecret {
	in, err := parseKeyArgs(args)
	if err != nil {
		return nil
	}
	secret, err := Extract(in.key, nil, in.hash)
	if err != nil {
		return nil
	}
	return &EarlySecret{
		secret: secret,
		hash:   in.hash,
		typ:    in.typ,
		typSet: in.typSet,
	}
}

// NewKeySchedule is an alias for NewEarlySecret.
func NewKeySchedule(args ...any) *KeySchedule { return NewEarlySecret(args...) }

// NewTLS13KeySchedule is an alias for NewEarlySecret.
func NewTLS13KeySchedule(args ...any) *TLS13KeySchedule { return NewEarlySecret(args...) }

// NewPSKKeySchedule is an alias for NewEarlySecret.
func NewPSKKeySchedule(args ...any) *KeySchedule { return NewEarlySecret(args...) }

// Secret returns a copy of the early secret.
func (s *EarlySecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// Hash returns the hash constructor associated with the early secret.
func (s *EarlySecret) Hash() HashFunc {
	if s == nil || s.hash == nil {
		return sha256.New
	}
	return s.hash
}

// BinderKey derives the binder key for kind. If kind is omitted, the type
// supplied to NewEarlySecret is used, defaulting to external.
func (s *EarlySecret) BinderKey(kind ...any) []byte {
	if s == nil {
		return nil
	}
	typ := PSKExternal
	if s.typSet {
		typ = s.typ
	}
	if len(kind) > 0 {
		t, ok := resolvePSKType(kind[0])
		if !ok {
			return nil
		}
		typ = t
	}
	return s.binderKeyFor(typ)
}

// PSKBinderKey is an alias for BinderKey.
func (s *EarlySecret) PSKBinderKey(kind ...any) []byte { return s.BinderKey(kind...) }

// DeriveBinderKey is an alias for BinderKey.
func (s *EarlySecret) DeriveBinderKey(kind ...any) []byte { return s.BinderKey(kind...) }

// ExternalBinderKey derives the external PSK binder key.
func (s *EarlySecret) ExternalBinderKey() []byte {
	if s == nil {
		return nil
	}
	return s.binderKeyFor(PSKExternal)
}

// ExternalPSKBinderKey is an alias for ExternalBinderKey.
func (s *EarlySecret) ExternalPSKBinderKey() []byte { return s.ExternalBinderKey() }

// ResumptionBinderKey derives the resumption PSK binder key.
func (s *EarlySecret) ResumptionBinderKey() []byte {
	if s == nil {
		return nil
	}
	return s.binderKeyFor(PSKResumption)
}

// ResumptionPSKBinderKey is an alias for ResumptionBinderKey.
func (s *EarlySecret) ResumptionPSKBinderKey() []byte { return s.ResumptionBinderKey() }

// ImportedBinderKey derives the RFC 9258 imported PSK binder key.
func (s *EarlySecret) ImportedBinderKey() []byte {
	if s == nil {
		return nil
	}
	return s.binderKeyFor(PSKImported)
}

func (s *EarlySecret) binderKeyFor(typ PSKType) []byte {
	if !typ.Valid() {
		return nil
	}
	out, err := DeriveSecret(s.secret, typ.Label(), nil, s.Hash())
	if err != nil {
		return nil
	}
	return out
}

// FinishedKey derives the finished/binder MAC key for kind, defaulting to the
// type supplied to NewEarlySecret.
func (s *EarlySecret) FinishedKey(kind ...any) []byte {
	binderKey := s.BinderKey(kind...)
	if binderKey == nil {
		return nil
	}
	out, err := ExpandLabel(binderKey, "finished", nil, s.Hash()().Size(), s.Hash())
	if err != nil {
		return nil
	}
	return out
}
