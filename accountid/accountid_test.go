package accountid

import (
	"errors"
	"strings"
	"testing"
)

var (
	bankA = Institution{
		Code:          "ABK1",
		Name:          "Alpha Bank",
		Country:       "CN",
		Prefix:        "AB12",
		AccountLength: 10,
		AllowedTypes:  []ProxyType{ProxyTypePhone, ProxyTypeBankAcct},
	}
	bankB = Institution{
		Code:          "BBK2",
		Name:          "Beta Bank",
		Country:       "CN",
		Prefix:        "CD34",
		AccountLength: 10,
		AllowedTypes:  []ProxyType{ProxyTypeEmail, ProxyTypeBankAcct},
	}
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(bankA); err != nil {
		t.Fatalf("register bankA: %v", err)
	}
	if err := reg.Register(bankB); err != nil {
		t.Fatalf("register bankB: %v", err)
	}
	return reg
}

func mustGenerate(t *testing.T, inst Institution, typ ProxyType, account string) string {
	t.Helper()
	raw, err := Generate(inst, typ, account)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return raw
}

func replaceField(raw string, idx int, val string) string {
	fields := strings.Split(raw, "-")
	fields[idx] = val
	return strings.Join(fields, "-")
}

func field(raw string, idx int) string {
	return strings.Split(raw, "-")[idx]
}

func expectErr(t *testing.T, err error, sentinel error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error %v, got %v", sentinel, err)
	}
}

func TestValidIdentifierAttribution(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	id, err := Parse(reg, raw)
	if err != nil {
		t.Fatalf("Parse valid identifier: %v", err)
	}
	if id.Institution().Code != bankA.Code {
		t.Fatalf("attribution: got %q, want %q", id.Institution().Code, bankA.Code)
	}
	if id.Account != "1234567890" || id.Type != ProxyTypePhone || id.Country != "CN" {
		t.Fatalf("parsed fields wrong: %+v", id)
	}
	if id.String() != raw {
		t.Fatalf("round trip: got %q, want %q", id.String(), raw)
	}

	rawB := mustGenerate(t, bankB, ProxyTypeEmail, "8765432109")
	idB, err := Parse(reg, rawB)
	if err != nil {
		t.Fatalf("Parse valid identifier B: %v", err)
	}
	if idB.Institution().Code != bankB.Code {
		t.Fatalf("attribution B: got %q, want %q", idB.Institution().Code, bankB.Code)
	}
}

func TestPrefixSubstitutionRejected(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	// Attacker swaps only the routing prefix to bank B's, keeping the
	// institution code: registry binding detects the cross-prefix.
	swapped := replaceField(raw, 1, bankB.Prefix)
	_, err := Parse(reg, swapped)
	expectErr(t, err, ErrPrefixMismatch)

	// Attacker swaps prefix and institution code together but cannot
	// recompute the check digits: checksum catches the forgery.
	// Use a proxy type both institutions allow so only the checksum
	// can reject the forgery.
	rawAC := mustGenerate(t, bankA, ProxyTypeBankAcct, "1234567890")
	forged := replaceField(replaceField(rawAC, 1, bankB.Prefix), 2, bankB.Code)
	_, err = Parse(reg, forged)
	expectErr(t, err, ErrChecksum)
}

func TestCheckDigitForgeryRejected(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	cd := field(raw, 5)
	forged := cd
	if cd[0] == '0' {
		forged = "1" + string(cd[1])
	} else {
		forged = "0" + string(cd[1])
	}
	_, err := Parse(reg, replaceField(raw, 5, forged))
	expectErr(t, err, ErrChecksum)

	// Mutating the account number without fixing the checksum also fails.
	mutated := replaceField(raw, 4, "1234567891")
	_, err = Parse(reg, mutated)
	expectErr(t, err, ErrChecksum)
}

func TestLengthVariantsRejected(t *testing.T) {
	reg := testRegistry(t)

	// Even with a correctly recomputed checksum, an account number that
	// does not match the registered length is rejected: length is
	// validated independently of the checksum.
	for _, account := range []string{"123456789", "12345678901"} {
		body := bankA.Country + bankA.Prefix + bankA.Code + string(ProxyTypePhone) + account
		raw := strings.Join([]string{
			bankA.Country, bankA.Prefix, bankA.Code,
			string(ProxyTypePhone), account, checkDigits(body),
		}, "-")
		_, err := Parse(reg, raw)
		expectErr(t, err, ErrAccountLength)
	}

	// Generate enforces the registered length as well.
	if _, err := Generate(bankA, ProxyTypePhone, "12345"); !errors.Is(err, ErrAccountLength) {
		t.Fatalf("Generate short account: got %v", err)
	}
}

func TestUnknownInstitutionRejected(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	// Prefix that no institution registered.
	unknown := replaceField(raw, 1, "ZZ99")
	_, err := Parse(reg, unknown)
	expectErr(t, err, ErrUnknownPrefix)

	// Known prefix but an institution code that is not registered.
	bogusCode := replaceField(raw, 2, "XXXX")
	_, err = Parse(reg, bogusCode)
	expectErr(t, err, ErrPrefixMismatch)
}

func TestProxyTypeValidation(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	// Unknown proxy type.
	badType := replaceField(raw, 3, "XX")
	_, err := Parse(reg, badType)
	expectErr(t, err, ErrProxyType)

	// Known type the institution does not accept, with recomputed
	// checksum so only the type policy can reject it.
	body := bankA.Country + bankA.Prefix + bankA.Code + string(ProxyTypeEmail) + "1234567890"
	disallowed := strings.Join([]string{
		bankA.Country, bankA.Prefix, bankA.Code,
		string(ProxyTypeEmail), "1234567890", checkDigits(body),
	}, "-")
	_, err = Parse(reg, disallowed)
	expectErr(t, err, ErrProxyTypeNotAllowed)
}

func TestRegistryUpdate(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	// After unregistration the prefix is unknown.
	if !reg.Unregister(bankA.Prefix) {
		t.Fatal("Unregister returned false")
	}
	_, err := Parse(reg, raw)
	expectErr(t, err, ErrUnknownPrefix)

	// Re-registering the same prefix for a different institution must
	// not resurrect the old identifier: binding and checksum both fail.
	squatter := Institution{
		Code:          "EVL9",
		Name:          "Evil Corp",
		Country:       "CN",
		Prefix:        bankA.Prefix,
		AccountLength: 10,
		AllowedTypes:  []ProxyType{ProxyTypePhone},
	}
	if err := reg.Register(squatter); err != nil {
		t.Fatalf("register squatter: %v", err)
	}
	_, err = Parse(reg, raw)
	expectErr(t, err, ErrPrefixMismatch)

	// Identifiers minted for the new registrant parse and attribute
	// to the new institution.
	newRaw := mustGenerate(t, squatter, ProxyTypePhone, "1234567890")
	id, err := Parse(reg, newRaw)
	if err != nil {
		t.Fatalf("Parse after registry update: %v", err)
	}
	if id.Institution().Code != squatter.Code {
		t.Fatalf("attribution after update: got %q, want %q",
			id.Institution().Code, squatter.Code)
	}

	// Restoring the original institution makes the old identifier
	// valid again.
	if err := reg.Register(bankA); err != nil {
		t.Fatalf("re-register bankA: %v", err)
	}
	if _, err := Parse(reg, raw); err != nil {
		t.Fatalf("Parse after restore: %v", err)
	}
}

func TestCrossPrefixAttributionFollowsRegistry(t *testing.T) {
	reg := testRegistry(t)

	// A fully self-consistent identifier minted with bank B's prefix
	// and checksum attributes to bank B, never to bank A: attribution
	// is derived from the registry, not from any claimed field.
	raw := mustGenerate(t, bankB, ProxyTypeBankAcct, "1122334455")
	id, err := Parse(reg, raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Institution().Code != bankB.Code {
		t.Fatalf("attribution: got %q, want %q", id.Institution().Code, bankB.Code)
	}

	// The same account digits under bank A's prefix are a different
	// identifier and must not validate with bank B's check digits.
	mixed := replaceField(raw, 1, bankA.Prefix)
	_, err = Parse(reg, mixed)
	expectErr(t, err, ErrPrefixMismatch)
}

func TestFormatErrors(t *testing.T) {
	reg := testRegistry(t)
	raw := mustGenerate(t, bankA, ProxyTypePhone, "1234567890")

	cases := map[string]error{
		replaceField(raw, 0, "C1"):        ErrCountry,
		replaceField(raw, 1, "ab12"):      ErrPrefixFormat,
		replaceField(raw, 1, "AB1"):       ErrPrefixFormat,
		replaceField(raw, 2, "a"):         ErrInstitutionFormat,
		replaceField(raw, 4, "12345AB90"): ErrAccountCharset,
		replaceField(raw, 5, "1A"):        ErrCheckDigitFormat,
		"CN-AB12-ABK1-PH-1234567890":      ErrFormat,
	}
	for input, sentinel := range cases {
		if _, err := Parse(reg, input); !errors.Is(err, sentinel) {
			t.Errorf("Parse(%q): got %v, want %v", input, err, sentinel)
		}
	}
}
