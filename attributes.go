package netsec

import "reflect"

// AttributeType is a PKCS #11 CK_ATTRIBUTE_TYPE value.
//
// It is an alias for uint so values obtained from common Go PKCS #11
// bindings can be used without an explicit conversion.
type AttributeType = uint

// PKCS #11 key attribute types used by this package.
const (
	CKA_TRUSTED           AttributeType = 0x000000ce
	CKA_SENSITIVE         AttributeType = 0x00000103
	CKA_EXTRACTABLE       AttributeType = 0x00000162
	CKA_ENCRYPT           AttributeType = 0x00000164
	CKA_DECRYPT           AttributeType = 0x00000165
	CKA_WRAP              AttributeType = 0x00000182
	CKA_UNWRAP            AttributeType = 0x00000183
	CKA_WRAP_WITH_TRUSTED AttributeType = 0x00000210
)

// Attribute is one PKCS #11 attribute. Its shape deliberately matches the
// Attribute type used by common Go PKCS #11 bindings.
type Attribute struct {
	Type  AttributeType
	Value any
}

// Attributes is a convenience name for a set of key attributes.
type Attributes []Attribute

// BoolAttribute returns a boolean attribute.
func BoolAttribute(attributeType AttributeType, value bool) Attribute {
	return Attribute{Type: attributeType, Value: value}
}

// NewAttributes returns a copy of attrs.
func NewAttributes(attrs ...Attribute) Attributes {
	return Attributes(append([]Attribute(nil), attrs...))
}

// Get returns the value of the first attribute of the requested type.
func (attrs Attributes) Get(attributeType AttributeType) (any, bool) {
	for _, attr := range attrs {
		if attr.Type == attributeType {
			return attr.Value, true
		}
	}
	return nil, false
}

// Has reports whether the set contains an attribute of the requested type.
func (attrs Attributes) Has(attributeType AttributeType) bool {
	_, ok := attrs.Get(attributeType)
	return ok
}

// Bool returns the value of a boolean attribute. Missing attributes are false,
// which is the PKCS #11 default for the attributes used by this package.
func (attrs Attributes) Bool(attributeType AttributeType) (bool, bool) {
	value, ok := attrs.Get(attributeType)
	if !ok {
		return false, true
	}
	decoded, err := decodeBool(attributeType, value)
	if err != nil {
		return false, false
	}
	return decoded, true
}

// Clone returns a shallow copy of attrs.
func (attrs Attributes) Clone() Attributes {
	return append(Attributes(nil), attrs...)
}

type attributeSet map[AttributeType]any

func collectAttributes(attrs []Attribute) (attributeSet, error) {
	set := make(attributeSet, len(attrs))
	for _, attr := range attrs {
		if previous, exists := set[attr.Type]; exists {
			if !reflect.DeepEqual(previous, attr.Value) {
				return nil, policyError(ErrDuplicateAttribute,
					"attribute 0x%x is present with conflicting values", attr.Type)
			}
			continue
		}
		set[attr.Type] = attr.Value
	}
	return set, nil
}

var knownBooleanAttributes = [...]AttributeType{
	CKA_TRUSTED,
	CKA_SENSITIVE,
	CKA_EXTRACTABLE,
	CKA_ENCRYPT,
	CKA_DECRYPT,
	CKA_WRAP,
	CKA_UNWRAP,
	CKA_WRAP_WITH_TRUSTED,
}

func validateKnownBooleans(set attributeSet) error {
	for _, attributeType := range knownBooleanAttributes {
		if value, exists := set[attributeType]; exists {
			if _, err := decodeBool(attributeType, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func booleanValue(set attributeSet, attributeType AttributeType) (bool, error) {
	value, exists := set[attributeType]
	if !exists {
		return false, nil
	}
	return decodeBool(attributeType, value)
}

// decodeBool accepts the representations commonly used for PKCS #11
// CK_BBOOL values: Go bools (including named bool types), integer 0/1 values,
// and a one-byte []byte slice containing 0 or 1.
func decodeBool(attributeType AttributeType, value any) (bool, error) {
	if value == nil {
		return false, policyError(ErrInvalidAttributeValue,
			"attribute 0x%x has a nil value", attributeType)
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return checkedBool(attributeType, rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return checkedBool(attributeType, int64(rv.Uint()))
	case reflect.Array, reflect.Slice:
		if rv.Type().Elem().Kind() != reflect.Uint8 || rv.Len() != 1 {
			return false, invalidBool(attributeType, value)
		}
		return checkedBool(attributeType, int64(rv.Index(0).Uint()))
	default:
		return false, invalidBool(attributeType, value)
	}
}

func checkedBool(attributeType AttributeType, value int64) (bool, error) {
	switch value {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, policyError(ErrInvalidAttributeValue,
			"attribute 0x%x has non-boolean value %d", attributeType, value)
	}
}

func invalidBool(attributeType AttributeType, value any) error {
	return policyError(ErrInvalidAttributeValue,
		"attribute 0x%x has unsupported boolean value of type %T", attributeType, value)
}
