// Package accountid provides parsing and validation of proxy-routed
// account identifiers for the payment clearing service.
//
// An identifier has the canonical form:
//
//	CC-PPPP-IIII-TT-ACCOUNT...-DD
//
//	CC    ISO-style country code, 2 uppercase letters
//	PPPP  routing prefix, 4 uppercase alphanumerics, mapped to an
//	      institution by the Registry
//	IIII  institution code, must match the registry entry for PPPP
//	TT    proxy type (PH, EM, ID, AC)
//	ACCOUNT  institution account number, digits only, fixed length
//	      per institution
//	DD    mod-97 check digits computed over all preceding fields
//
// Validation is performed field by field: format, registry lookup,
// prefix/institution binding, proxy type, account length and finally
// the checksum. Unknown institutions and cross-prefix identifiers are
// rejected.
package accountid

import (
	"errors"
	"fmt"
	"strings"
)

// ProxyType identifies the kind of proxy an account is addressed by.
type ProxyType string

const (
	ProxyTypePhone    ProxyType = "PH"
	ProxyTypeEmail    ProxyType = "EM"
	ProxyTypeGovID    ProxyType = "ID"
	ProxyTypeBankAcct ProxyType = "AC"
)

// Valid reports whether t is a known proxy type.
func (t ProxyType) Valid() bool {
	switch t {
	case ProxyTypePhone, ProxyTypeEmail, ProxyTypeGovID, ProxyTypeBankAcct:
		return true
	}
	return false
}

// Sentinel validation errors. Use errors.Is to match them.
var (
	ErrFormat              = errors.New("accountid: malformed identifier")
	ErrCountry             = errors.New("accountid: invalid country code")
	ErrPrefixFormat        = errors.New("accountid: invalid routing prefix format")
	ErrInstitutionFormat   = errors.New("accountid: invalid institution code format")
	ErrProxyType           = errors.New("accountid: unknown proxy type")
	ErrProxyTypeNotAllowed = errors.New("accountid: proxy type not allowed for institution")
	ErrAccountCharset      = errors.New("accountid: account number must be digits")
	ErrAccountLength       = errors.New("accountid: account number length mismatch")
	ErrCheckDigitFormat    = errors.New("accountid: invalid check digit format")
	ErrChecksum            = errors.New("accountid: check digit verification failed")
	ErrUnknownPrefix       = errors.New("accountid: routing prefix not in registry")
	ErrPrefixMismatch      = errors.New("accountid: prefix routes to a different institution")
	ErrCountryMismatch     = errors.New("accountid: country does not match registry entry")
)

const (
	countryLen  = 2
	prefixLen   = 4
	typeLen     = 2
	checkLen    = 2
	fieldCount  = 6
	minInstCode = 2
	maxInstCode = 8
)

// Identifier is a parsed and fully validated account identifier.
type Identifier struct {
	Country         string
	Prefix          string
	InstitutionCode string
	Type            ProxyType
	Account         string
	CheckDigits     string

	institution Institution
}

// Institution returns the registry entry the identifier resolved to.
// Account attribution always follows this value.
func (id *Identifier) Institution() Institution { return id.institution }

// String returns the canonical textual form of the identifier.
func (id *Identifier) String() string {
	return strings.Join([]string{
		id.Country, id.Prefix, id.InstitutionCode,
		string(id.Type), id.Account, id.CheckDigits,
	}, "-")
}

// body returns the concatenated fields covered by the check digits.
func (id *Identifier) body() string {
	return id.Country + id.Prefix + id.InstitutionCode + string(id.Type) + id.Account
}

// Parse validates raw field by field and resolves the institution from
// reg. It returns a descriptive error wrapping one of the sentinel
// errors on any failure.
func Parse(reg *Registry, raw string) (*Identifier, error) {
	if reg == nil {
		return nil, fmt.Errorf("accountid: nil registry")
	}
	fields := strings.Split(raw, "-")
	if len(fields) != fieldCount {
		return nil, fmt.Errorf("%w: want %d fields, got %d", ErrFormat, fieldCount, len(fields))
	}
	id := &Identifier{
		Country:         fields[0],
		Prefix:          fields[1],
		InstitutionCode: fields[2],
		Type:            ProxyType(fields[3]),
		Account:         fields[4],
		CheckDigits:     fields[5],
	}

	if !isUpperLetters(id.Country, countryLen) {
		return nil, fmt.Errorf("%w: %q", ErrCountry, id.Country)
	}
	if !isUpperAlnum(id.Prefix, prefixLen) {
		return nil, fmt.Errorf("%w: %q", ErrPrefixFormat, id.Prefix)
	}
	if len(id.InstitutionCode) < minInstCode || len(id.InstitutionCode) > maxInstCode ||
		!isUpperAlnum(id.InstitutionCode, len(id.InstitutionCode)) {
		return nil, fmt.Errorf("%w: %q", ErrInstitutionFormat, id.InstitutionCode)
	}
	if len(string(id.Type)) != typeLen || !id.Type.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrProxyType, string(id.Type))
	}
	if !isDigits(id.Account) {
		return nil, fmt.Errorf("%w: %q", ErrAccountCharset, id.Account)
	}
	if !isDigits(id.CheckDigits) || len(id.CheckDigits) != checkLen {
		return nil, fmt.Errorf("%w: %q", ErrCheckDigitFormat, id.CheckDigits)
	}

	inst, ok := reg.LookupPrefix(id.Prefix)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPrefix, id.Prefix)
	}
	if inst.Code != id.InstitutionCode {
		return nil, fmt.Errorf("%w: prefix %q belongs to %q, not %q",
			ErrPrefixMismatch, id.Prefix, inst.Code, id.InstitutionCode)
	}
	if inst.Country != id.Country {
		return nil, fmt.Errorf("%w: prefix %q is registered under %q",
			ErrCountryMismatch, id.Prefix, inst.Country)
	}
	if !inst.Allows(id.Type) {
		return nil, fmt.Errorf("%w: %q for institution %q",
			ErrProxyTypeNotAllowed, string(id.Type), inst.Code)
	}
	if len(id.Account) != inst.AccountLength {
		return nil, fmt.Errorf("%w: institution %q requires %d digits, got %d",
			ErrAccountLength, inst.Code, inst.AccountLength, len(id.Account))
	}
	if !verifyChecksum(id.body(), id.CheckDigits) {
		return nil, fmt.Errorf("%w: %q", ErrChecksum, raw)
	}

	id.institution = inst
	return id, nil
}

// Generate builds a canonical identifier for account at inst. It fails
// if the proxy type is not allowed for the institution or the account
// number does not match the registered length.
func Generate(inst Institution, typ ProxyType, account string) (string, error) {
	if !typ.Valid() {
		return "", fmt.Errorf("%w: %q", ErrProxyType, string(typ))
	}
	if !inst.Allows(typ) {
		return "", fmt.Errorf("%w: %q for institution %q",
			ErrProxyTypeNotAllowed, string(typ), inst.Code)
	}
	if !isDigits(account) {
		return "", fmt.Errorf("%w: %q", ErrAccountCharset, account)
	}
	if len(account) != inst.AccountLength {
		return "", fmt.Errorf("%w: institution %q requires %d digits, got %d",
			ErrAccountLength, inst.Code, inst.AccountLength, len(account))
	}
	id := &Identifier{
		Country:         inst.Country,
		Prefix:          inst.Prefix,
		InstitutionCode: inst.Code,
		Type:            typ,
		Account:         account,
	}
	id.CheckDigits = checkDigits(id.body())
	return id.String(), nil
}

func isUpperLetters(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func isUpperAlnum(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
