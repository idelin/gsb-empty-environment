package netsec

// PSKIdentity is one entry in the client's pre_shared_key identity list.
//
// Identity is the opaque identity value sent on the wire.
type PSKIdentity struct {
	Identity            []byte
	ObfuscatedTicketAge uint32
}

// Bytes returns the wire identity for i.
func (i PSKIdentity) Bytes() []byte {
	return i.Identity
}

// IdentityBytes is an alias for Bytes, for callers that prefer an explicit
// accessor name.
func (i PSKIdentity) IdentityBytes() []byte {
	return i.Bytes()
}

// NewPSKIdentity constructs an identity, optionally with an obfuscated ticket
// age.
func NewPSKIdentity(identity []byte, obfuscatedTicketAge ...uint32) PSKIdentity {
	var age uint32
	if len(obfuscatedTicketAge) > 0 {
		age = obfuscatedTicketAge[0]
	}
	return PSKIdentity{
		Identity:            append([]byte(nil), identity...),
		ObfuscatedTicketAge: age,
	}
}

// LabeledPSKIdentity is a compatibility form for TLS implementations that
// call the identity bytes a label, as crypto/tls does internally.
type LabeledPSKIdentity struct {
	Label               []byte
	ObfuscatedTicketAge uint32
}

// Bytes returns the wire identity for i.
func (i LabeledPSKIdentity) Bytes() []byte {
	return i.Label
}

// NewLabeledPSKIdentity constructs a LabeledPSKIdentity.
func NewLabeledPSKIdentity(label []byte, obfuscatedTicketAge ...uint32) LabeledPSKIdentity {
	var age uint32
	if len(obfuscatedTicketAge) > 0 {
		age = obfuscatedTicketAge[0]
	}
	return LabeledPSKIdentity{
		Label:               append([]byte(nil), label...),
		ObfuscatedTicketAge: age,
	}
}

// Identity is a shorter spelling for PSKIdentity.
type Identity = PSKIdentity

// PskIdentity is an alternative capitalization of PSKIdentity.
type PskIdentity = PSKIdentity

// PreSharedIdentity is an alternative spelling of PSKIdentity.
type PreSharedIdentity = PSKIdentity

// PresharedIdentity is an alternative spelling of PSKIdentity.
type PresharedIdentity = PSKIdentity

// PSKBinder is one TLS 1.3 pre_shared_key binder entry.
type PSKBinder []byte

// PSKBinderEntry is an alternative spelling of PSKBinder.
type PSKBinderEntry = PSKBinder

// PSKOffer is the client-side state needed to validate a server's
// selected_identity value. Binders contains one binder per identity, in the
// same order as Identities.
type PSKOffer struct {
	Identities []PSKIdentity
	Binders    [][]byte

	// Binding, when non-nil, is the expected binder for the selected
	// identity. It is checked even when SelectedIdentity is zero.
	Binding []byte
}

// PSK is a shorter spelling for PSKOffer.
type PSK = PSKOffer

// PreSharedKey is an alternative spelling for PSKOffer.
type PreSharedKey = PSKOffer

// PSKSelection is an alternative spelling for PSKOffer.
type PSKSelection = PSKOffer

// ValidateSelectedIdentity checks a server-selected index against the offer.
func (o PSKOffer) ValidateSelectedIdentity(selectedIdentity any) error {
	return ValidateSelectedIdentity(selectedIdentity, o)
}

// Check is an alias for ValidateSelectedIdentity.
func (o PSKOffer) Check(selectedIdentity any) error {
	return o.ValidateSelectedIdentity(selectedIdentity)
}

// Validate is an alias for ValidateSelectedIdentity.
func (o PSKOffer) Validate(selectedIdentity any) error {
	return o.ValidateSelectedIdentity(selectedIdentity)
}
