package netsec

// sensitiveAttributes enumerates attributes that carry key material. When
// an object has CKA_SENSITIVE set, requesting any of these is refused
// rather than forwarding the call. CKA_VALUE is the attribute mandated by
// the policy; the PKCS#11 private-key fields hold the same kind of secret
// and are treated identically.
var sensitiveAttributes = map[AttributeType]bool{
	CKA_VALUE:            true,
	CKA_PRIVATE_EXPONENT: true,
	CKA_PRIME_1:          true,
	CKA_PRIME_2:          true,
	CKA_EXPONENT_1:       true,
	CKA_EXPONENT_2:       true,
	CKA_COEFFICIENT:      true,
}

// Guard is a call interceptor placed in front of a PKCS#11 proxy
// forwarding path. Every intercepted method enforces non-exportability
// policy before touching the backend. Policy decisions query the trusted
// backend for the object's current attributes; the guard keeps no object
// state of its own, so session objects are protected exactly like token
// objects.
type Guard struct {
	backend Backend
}

// NewGuard builds a Guard that enforces policy on backend.
func NewGuard(backend Backend) *Guard {
	return &Guard{backend: backend}
}

// GetAttributeValue intercepts C_GetAttributeValue. When the target object
// has CKA_SENSITIVE=true, requests for secret-bearing attributes (notably
// CKA_VALUE) are denied with ErrAttributeSensitive and never forwarded.
// Non-sensitive attributes pass through to the backend unchanged.
func (g *Guard) GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error) {
	secretRequested := false
	for _, attr := range attrs {
		if sensitiveAttributes[attr] {
			secretRequested = true
			break
		}
	}

	if secretRequested {
		sensitive, err := g.boolAttribute(session, object, CKA_SENSITIVE)
		if err != nil {
			return nil, err
		}
		if sensitive {
			return nil, ErrAttributeSensitive
		}
	}

	return g.backend.GetAttributeValue(session, object, attrs)
}

// WrapKey intercepts C_WrapKey. An object whose CKA_EXTRACTABLE attribute
// is false must never leave the HSM as wrapped key material, so the call
// is denied with ErrKeyUnextractable.
func (g *Guard) WrapKey(session SessionHandle, wrappingKey ObjectHandle, key ObjectHandle, mechanism Mechanism) ([]byte, error) {
	extractable, err := g.boolAttribute(session, key, CKA_EXTRACTABLE)
	if err != nil {
		return nil, err
	}
	if !extractable {
		return nil, ErrKeyUnextractable
	}

	return g.backend.WrapKey(session, wrappingKey, key, mechanism)
}

// CopyObject intercepts C_CopyObject. A template that attempts to turn
// CKA_EXTRACTABLE from false into true on the copy is rejected with
// ErrTemplateInconsistent so the attribute cannot be escalated through a
// copy. Other templates, including copies that stay non-extractable, are
// forwarded.
func (g *Guard) CopyObject(session SessionHandle, object ObjectHandle, template []Attribute) (ObjectHandle, error) {
	if err := g.denyExtractableFlip(session, object, template); err != nil {
		return 0, err
	}

	return g.backend.CopyObject(session, object, template)
}

// SetAttributeValue intercepts C_SetAttributeValue. Flipping
// CKA_EXTRACTABLE from false to true in place is rejected with
// ErrTemplateInconsistent.
func (g *Guard) SetAttributeValue(session SessionHandle, object ObjectHandle, template []Attribute) error {
	if err := g.denyExtractableFlip(session, object, template); err != nil {
		return err
	}

	return g.backend.SetAttributeValue(session, object, template)
}

// denyExtractableFlip implements the CKA_EXTRACTABLE false -> true rule
// shared by CopyObject and SetAttributeValue.
func (g *Guard) denyExtractableFlip(session SessionHandle, object ObjectHandle, template []Attribute) error {
	requested, present, err := templateBool(template, CKA_EXTRACTABLE)
	if err != nil {
		return err
	}
	if !present || !requested {
		return nil
	}

	current, err := g.boolAttribute(session, object, CKA_EXTRACTABLE)
	if err != nil {
		return err
	}
	if !current {
		return ErrTemplateInconsistent
	}

	return nil
}

// boolAttribute reads a single boolean PKCS#11 attribute from the backend.
// Any failure to read or decode the attribute makes the caller fail closed.
func (g *Guard) boolAttribute(session SessionHandle, object ObjectHandle, attr AttributeType) (bool, error) {
	values, err := g.backend.GetAttributeValue(session, object, []AttributeType{attr})
	if err != nil {
		return false, err
	}

	for _, value := range values {
		if value.Type != attr {
			continue
		}
		decoded, decodeErr := decodeCKBool(value.Value)
		if decodeErr != nil {
			return false, decodeErr
		}
		return decoded, nil
	}

	return false, ErrAttributeTypeInvalid
}

// decodeCKBool decodes a PKCS#11 CK_BBOOL: exactly one byte, 0x00 (false)
// or 0x01 (true). Anything else is a protocol error.
func decodeCKBool(raw []byte) (bool, error) {
	if len(raw) != 1 {
		return false, ErrAttributeTypeInvalid
	}
	switch raw[0] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, ErrAttributeTypeInvalid
	}
}

// templateBool looks up attr in a template. present is false when the
// template does not contain the attribute.
func templateBool(template []Attribute, attr AttributeType) (value bool, present bool, err error) {
	for _, entry := range template {
		if entry.Type != attr {
			continue
		}
		decoded, decodeErr := decodeCKBool(entry.Value)
		if decodeErr != nil {
			return false, true, decodeErr
		}
		return decoded, true, nil
	}
	return false, false, nil
}
