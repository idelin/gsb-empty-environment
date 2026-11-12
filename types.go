package netsec

// AttributeType identifies a PKCS#11 CKA_* object attribute.
type AttributeType uint

// PKCS#11 attribute types referenced by the guard. Values match the
// PKCS#11 specification so wire-format templates can be passed through
// without translation.
const (
	CKA_CLASS            AttributeType = 0x0000
	CKA_TOKEN            AttributeType = 0x0001
	CKA_PRIVATE          AttributeType = 0x0002
	CKA_LABEL            AttributeType = 0x0003
	CKA_KEY_TYPE         AttributeType = 0x0100
	CKA_SENSITIVE        AttributeType = 0x0010
	CKA_VALUE            AttributeType = 0x0011
	CKA_MODULUS          AttributeType = 0x0120
	CKA_PUBLIC_EXPONENT  AttributeType = 0x0122
	CKA_PRIVATE_EXPONENT AttributeType = 0x0121
	CKA_PRIME_1          AttributeType = 0x0123
	CKA_PRIME_2          AttributeType = 0x0124
	CKA_EXPONENT_1       AttributeType = 0x0125
	CKA_EXPONENT_2       AttributeType = 0x0126
	CKA_COEFFICIENT      AttributeType = 0x0127
	CKA_EXTRACTABLE      AttributeType = 0x0162
)

// PKCS#11 return values used when the guard denies a call.
const (
	CKR_FUNCTION_FAILED        uint = 0x06
	CKR_ARGUMENTS_BAD          uint = 0x07
	CKR_ATTRIBUTE_SENSITIVE    uint = 0x11
	CKR_ATTRIBUTE_TYPE_INVALID uint = 0x12
	CKR_KEY_UNEXTRACTABLE      uint = 0x67
	CKR_OBJECT_HANDLE_INVALID  uint = 0x82
	CKR_TEMPLATE_INCONSISTENT  uint = 0xD1
)

// ObjectHandle is a PKCS#11 CK_OBJECT_HANDLE.
type ObjectHandle uint

// SessionHandle is a PKCS#11 CK_SESSION_HANDLE.
type SessionHandle uint

// Attribute is one entry of a PKCS#11 attribute template.
type Attribute struct {
	Type  AttributeType
	Value []byte
}

// Mechanism describes a key wrapping mechanism (CK_MECHANISM).
type Mechanism struct {
	Mechanism uint
	Parameter []byte
}

// Error carries a PKCS#11 CKR_* return value. Two Errors are considered
// equal by errors.Is when their codes match, even across wrapping.
type Error struct {
	Code    uint
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "netsec: pkcs#11 error 0x" + hexByte(e.Code)
	}
	return "netsec: " + e.Message
}

// Is compares Error values by PKCS#11 return code.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other.Code == e.Code
}

func hexByte(v uint) string {
	const digits = "0123456789abcdef"
	if v == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v&0xf]
		v >>= 4
	}
	return string(buf[i:])
}

// Sentinels returned when a call is blocked by policy or malformed.
var (
	// ErrAttributeSensitive is returned instead of disclosing secret
	// material of a CKA_SENSITIVE object (CKR_ATTRIBUTE_SENSITIVE).
	ErrAttributeSensitive = &Error{
		Code:    CKR_ATTRIBUTE_SENSITIVE,
		Message: "attribute is sensitive and must not be disclosed",
	}

	// ErrKeyUnextractable is returned when C_WrapKey targets an object
	// whose CKA_EXTRACTABLE attribute is false (CKR_KEY_UNEXTRACTABLE).
	ErrKeyUnextractable = &Error{
		Code:    CKR_KEY_UNEXTRACTABLE,
		Message: "object is not extractable and cannot be wrapped",
	}

	// ErrTemplateInconsistent is returned when a template would flip
	// CKA_EXTRACTABLE from false to true, or is malformed
	// (CKR_TEMPLATE_INCONSISTENT).
	ErrTemplateInconsistent = &Error{
		Code:    CKR_TEMPLATE_INCONSISTENT,
		Message: "template would change CKA_EXTRACTABLE from false to true",
	}

	// ErrObjectHandleInvalid is returned when the backend cannot resolve
	// the referenced object (CKR_OBJECT_HANDLE_INVALID).
	ErrObjectHandleInvalid = &Error{
		Code:    CKR_OBJECT_HANDLE_INVALID,
		Message: "object handle invalid",
	}

	// ErrAttributeTypeInvalid is returned when a required boolean
	// attribute cannot be read or decoded, so the call fails closed.
	ErrAttributeTypeInvalid = &Error{
		Code:    CKR_ATTRIBUTE_TYPE_INVALID,
		Message: "required boolean attribute is missing or invalid",
	}
)

// Backend is the trusted downstream PKCS#11 implementation (for example a
// real HSM binding) that receives calls after the guard approves them.
// Implementations must be safe for concurrent use.
type Backend interface {
	// GetAttributeValue reads object attributes from the backend.
	GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error)

	// WrapKey wraps key with wrappingKey using mechanism.
	WrapKey(session SessionHandle, wrappingKey ObjectHandle, key ObjectHandle, mechanism Mechanism) ([]byte, error)

	// CopyObject duplicates object using template.
	CopyObject(session SessionHandle, object ObjectHandle, template []Attribute) (ObjectHandle, error)

	// SetAttributeValue updates object attributes from template.
	SetAttributeValue(session SessionHandle, object ObjectHandle, template []Attribute) error
}
