package netsec

import (
	"errors"
	"testing"
)

func q0211TestIdentities() []PSKIdentity {
	return []PSKIdentity{
		{Identity: []byte("first"), ObfuscatedTicketAge: 1},
		{Identity: []byte("second"), ObfuscatedTicketAge: 2},
	}
}

func TestQ0211SelectedIdentityMustBeInIdentityList(t *testing.T) {
	identities := q0211TestIdentities()
	tests := []struct {
		name  string
		index int
	}{
		{name: "list length", index: len(identities)},
		{name: "past list length", index: len(identities) + 7},
		{name: "negative", index: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSelectedIdentity(tt.index, identities)
			if err == nil {
				t.Fatal("expected an invalid selected_identity error")
			}
			if !errors.Is(err, ErrInvalidSelectedIdentity) {
				t.Fatalf("errors.Is(%v, ErrInvalidSelectedIdentity) = false", err)
			}
		})
	}
}

func TestQ0211EmptyIdentityListRejectsZero(t *testing.T) {
	if err := ValidateSelectedIdentity(0, []PSKIdentity{}); err == nil {
		t.Fatal("index zero must not select an identity from an empty list")
	}
	if err := ValidateSelectedIdentity(0, 0); err == nil {
		t.Fatal("index zero must be out of range for a zero identity count")
	}
	if err := ValidateSelectedIdentityIndex(0, 0); err == nil {
		t.Fatal("index zero must be out of range for a zero identity count")
	}
}

func TestQ0211ZeroIndexContinuesToBinderCheck(t *testing.T) {
	identities := q0211TestIdentities()
	binders := [][]byte{[]byte("binder-zero"), []byte("binder-one")}

	if err := ValidateSelectedIdentity(0, identities, binders); err != nil {
		t.Fatalf("valid index zero rejected: %v", err)
	}
	if err := ValidateSelectedIdentity(0, identities, binders, binders[0]); err != nil {
		t.Fatalf("matching binder rejected: %v", err)
	}

	err := ValidateSelectedIdentity(0, identities, binders, []byte("different"))
	if err == nil {
		t.Fatal("mismatched binder for index zero was accepted")
	}
	if !errors.Is(err, ErrInvalidPSKBinder) {
		t.Fatalf("errors.Is(%v, ErrInvalidPSKBinder) = false", err)
	}
}

func TestQ0211MissingSelectedBinderRejected(t *testing.T) {
	identities := q0211TestIdentities()
	if err := ValidateSelectedIdentity(0, identities, [][]byte{[]byte("only-one")}, []byte("only-one")); err == nil {
		t.Fatal("binder list shorter than identity list was accepted")
	}
	if err := ValidateSelectedIdentity(0, identities, [][]byte{{}, []byte("b1")}); err == nil {
		t.Fatal("empty binder for the selected identity was accepted")
	}

	offer := PSKOffer{
		Identities: identities,
		Binding:    []byte("expected"),
	}
	if err := offer.ValidateSelectedIdentity(0); err == nil {
		t.Fatal("missing binder list was accepted")
	}
}

func TestQ0211RawIdentityAndBinderSlices(t *testing.T) {
	identities := [][]byte{[]byte("first"), []byte("second")}
	binders := [][]byte{[]byte("b0"), []byte("b1")}

	if err := ValidateSelectedIdentity(1, identities, binders, binders[1]); err != nil {
		t.Fatalf("valid raw slices rejected: %v", err)
	}
	if err := ValidateSelectedIdentity(1, identities, binders, "b1"); err != nil {
		t.Fatalf("valid string binding rejected: %v", err)
	}
	if err := ValidateSelectedIdentity(2, identities, binders); err == nil {
		t.Fatal("raw index equal to list length was accepted")
	}
}

func TestQ0211ServerHelloAdapter(t *testing.T) {
	identities := q0211TestIdentities()
	binders := [][]byte{[]byte("b0"), []byte("b1")}
	clientHello := ClientHello{PSKIdentities: identities, PSKBinders: binders}

	if err := ValidateServerHelloSelectedIdentity(ServerHello{SelectedIdentity: 0}, clientHello); err != nil {
		t.Fatalf("valid selected identity rejected: %v", err)
	}
	if err := ValidateServerHelloSelectedIdentity(ServerHello{SelectedIdentity: uint16(len(identities))}, clientHello); err == nil {
		t.Fatal("out-of-range selected identity was accepted")
	}
}

func TestQ0211PSKOfferBinding(t *testing.T) {
	identities := q0211TestIdentities()
	binders := [][]byte{[]byte("b0"), []byte("b1")}
	offer := PSKOffer{
		Identities: identities,
		Binders:    binders,
		Binding:    binders[0],
	}
	if err := offer.Check(0); err != nil {
		t.Fatalf("valid offer rejected: %v", err)
	}

	offer.Binding = []byte("wrong")
	if err := offer.Check(0); !errors.Is(err, ErrInvalidPSKBinder) {
		t.Fatalf("wrong binding error = %v, want ErrInvalidPSKBinder", err)
	}
}

func TestQ0211IdentityEmbeddedBinding(t *testing.T) {
	type entry struct {
		Identity []byte
		Binding  []byte
	}
	identities := []entry{
		{Identity: []byte("first"), Binding: []byte("b0")},
		{Identity: []byte("second"), Binding: []byte("b1")},
	}
	if err := ValidateSelectedIdentity(0, identities, []byte("b0")); err != nil {
		t.Fatalf("embedded binding rejected: %v", err)
	}
	if err := ValidateSelectedIdentity(0, identities, []byte("wrong")); !errors.Is(err, ErrInvalidPSKBinder) {
		t.Fatalf("embedded binding mismatch error = %v, want ErrInvalidPSKBinder", err)
	}
}

func TestQ0211CompatibilitySpellings(t *testing.T) {
	identities := q0211TestIdentities()
	if err := CheckSelectedIdentity(len(identities), identities); err == nil {
		t.Fatal("CheckSelectedIdentity accepted an out-of-range index")
	}
	if err := ValidatePSKIdentity(len(identities), identities); err == nil {
		t.Fatal("ValidatePSKIdentity accepted an out-of-range index")
	}
	if !IsSelectedIdentityValid(0, identities) {
		t.Fatal("IsSelectedIdentityValid rejected index zero")
	}
	if !SelectedIdentityInRange(1, identities) {
		t.Fatal("SelectedIdentityInRange rejected a valid index")
	}
}

func TestQ0211ErrorMessagesMatchTLS(t *testing.T) {
	if got := ErrInvalidSelectedIdentity.Error(); got != "tls: server selected an invalid PSK" {
		t.Fatalf("unexpected selected identity error: %q", got)
	}
	if got := ErrInvalidPSKBinder.Error(); got != "tls: invalid PSK binder" {
		t.Fatalf("unexpected binder error: %q", got)
	}
	if err := ValidateSelectedIdentity(9, q0211TestIdentities()); err != ErrInvalidSelectedIdentity {
		t.Fatalf("out-of-range error identity = %v, want sentinel", err)
	}
}
