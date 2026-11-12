package netsec

// WriteAccess is one object/property write inside a WritePropertyMultiple
// request. Priority uses the BACnet priority array, where 1 is the highest
// priority.
type WriteAccess struct {
	Object   ObjectID
	Property PropertyID
	Priority uint8
}

// Request is the normalized form the forwarding loop hands to the policy.
//
// For ServiceWriteProperty, Object, Property and Priority describe the single
// write. For ServiceWritePropertyMultiple, Writes carries every requested
// write and the top-level object/property fields are ignored. A priority of
// zero means that the request did not carry a priority.
type Request struct {
	Service Service

	Object   ObjectID
	Property PropertyID
	Priority uint8

	Writes []WriteAccess
}
