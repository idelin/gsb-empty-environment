package netsec

import (
	"fmt"
)

// ObjectType is the 10-bit BACnet object type.
type ObjectType uint16

// Common BACnet object types. The full set is intentionally not duplicated
// here; any value in [0, 1023] is accepted by the policy.
const (
	ObjectTypeAnalogInput  ObjectType = 0
	ObjectTypeAnalogOutput ObjectType = 1
	ObjectTypeAnalogValue  ObjectType = 2
	ObjectTypeBinaryInput  ObjectType = 3
	ObjectTypeBinaryOutput ObjectType = 4
	ObjectTypeBinaryValue  ObjectType = 5
	ObjectTypeDevice       ObjectType = 8
)

// ObjectID identifies a BACnet object: a 10-bit type and a 22-bit instance.
type ObjectID struct {
	Type     ObjectType
	Instance uint32
}

// NewObjectID constructs and validates an object identifier.
func NewObjectID(objectType ObjectType, instance uint32) (ObjectID, error) {
	id := ObjectID{Type: objectType, Instance: instance}
	if err := id.Validate(); err != nil {
		return ObjectID{}, err
	}
	return id, nil
}

// Validate reports whether the object identifier fits the BACnet wire format.
func (id ObjectID) Validate() error {
	if id.Type > 1023 {
		return fmt.Errorf("%w: object type %d exceeds 10 bits", ErrInvalidTarget, id.Type)
	}
	if id.Instance > 0x3fffff {
		return fmt.Errorf("%w: object instance %d exceeds 22 bits", ErrInvalidTarget, id.Instance)
	}
	return nil
}

func (id ObjectID) String() string {
	return fmt.Sprintf("%d:%d", id.Type, id.Instance)
}

// PropertyID is the 22-bit BACnet property identifier.
type PropertyID uint32

// Common BACnet properties involved in commandable-value writes.
const (
	PropertyOutOfService      PropertyID = 81
	PropertyPresentValue      PropertyID = 85
	PropertyPriorityArray     PropertyID = 87
	PropertyRelinquishDefault PropertyID = 104
)

// Validate reports whether the property identifier fits the BACnet wire
// format.
func (p PropertyID) Validate() error {
	if p > 0x3fffff {
		return fmt.Errorf("%w: property id %d exceeds 22 bits", ErrInvalidTarget, p)
	}
	return nil
}

func (p PropertyID) String() string {
	return fmt.Sprintf("%d", uint32(p))
}

// Target is one object/property pair. The admission policy grants writes only
// to exactly registered pairs; there is no wildcard matching.
type Target struct {
	Object   ObjectID
	Property PropertyID
}

// NewTarget constructs a validated object/property pair.
func NewTarget(objectType ObjectType, instance uint32, property PropertyID) (Target, error) {
	target := Target{
		Object:   ObjectID{Type: objectType, Instance: instance},
		Property: property,
	}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

// Validate reports whether both parts of the target are valid BACnet
// identifiers.
func (t Target) Validate() error {
	if err := t.Object.Validate(); err != nil {
		return err
	}
	return t.Property.Validate()
}

func (t Target) String() string {
	return fmt.Sprintf("%s/%s", t.Object, t.Property)
}
