package netsec

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Address is a single RFC 5322 mailbox together with its raw, unencoded display
// name. NewAddress and ParseAddress are the preferred constructors. Every
// serialization method validates the fields again so a directly constructed
// value cannot bypass the checks.
type Address struct {
	Name    string
	Address string
}

// NewAddress validates a raw display name and mailbox. The display name must be
// valid UTF-8 without line breaks or control characters; non-ASCII characters
// are accepted and later encoded as RFC 2047 encoded-words. The mailbox itself
// must be a single ASCII-only addr-spec.
func NewAddress(name, address string) (Address, error) {
	a := Address{Name: name, Address: address}
	if err := a.Validate(); err != nil {
		return Address{}, err
	}
	return a, nil
}

// ParseAddress parses one RFC 5322 address. Encoded display names are decoded
// by net/mail exactly once, then validated as raw text. Use NewAddress when the
// display name has not been encoded.
func ParseAddress(value string) (Address, error) {
	if err := validateText(value, "address"); err != nil {
		return Address{}, err
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return Address{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	return NewAddress(parsed.Name, parsed.Address)
}

// ParseAddressList parses a comma-separated list of RFC 5322 addresses. The
// complete input is checked for line breaks and controls before the parser is
// invoked, and every decoded address is validated individually.
func ParseAddressList(value string) ([]Address, error) {
	if err := validateText(value, "address list"); err != nil {
		return nil, err
	}
	parsed, err := mail.ParseAddressList(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	addresses := make([]Address, 0, len(parsed))
	for _, item := range parsed {
		addr, err := NewAddress(item.Name, item.Address)
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// Validate checks the address without producing output.
func (a Address) Validate() error {
	if err := validateText(a.Name, "display name"); err != nil {
		return err
	}
	if err := rejectPreEncoded(a.Name); err != nil {
		return err
	}
	if err := validateMailbox(a.Address); err != nil {
		return err
	}
	return nil
}

// Encode returns a safe RFC 5322 mailbox suitable for a structured address
// header. Non-ASCII and overlong display names are represented as folded RFC
// 2047 B-encoded words.
func (a Address) Encode() (string, error) {
	tokens, err := a.headerTokens()
	if err != nil {
		return "", err
	}
	return strings.Join(tokens, " "), nil
}

// String returns the encoded mailbox, or an empty string if the address is
// invalid. Prefer Encode when an error must be handled.
func (a Address) String() string {
	encoded, err := a.Encode()
	if err != nil {
		return ""
	}
	return encoded
}

// FormatAddress is the error-returning package-level form of Address.Encode.
func FormatAddress(a Address) (string, error) {
	return a.Encode()
}

func (a Address) headerTokens() ([]string, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if a.Name == "" {
		return []string{a.Address}, nil
	}
	nameTokens := encodePhrase(a.Name)
	return append(nameTokens, "<"+a.Address+">"), nil
}

func validateMailbox(address string) error {
	if address == "" {
		return fmt.Errorf("%w: empty mailbox", ErrInvalidAddress)
	}
	if len(address) > 254 {
		return fmt.Errorf("%w: mailbox is too long", ErrInvalidAddress)
	}
	for i := 0; i < len(address); i++ {
		b := address[i]
		if b >= utf8.RuneSelf {
			return fmt.Errorf("%w: non-ASCII mailbox must use SMTPUTF8, which is not supported", ErrInvalidAddress)
		}
		if b < 0x21 || b == 0x7f {
			return fmt.Errorf("%w: mailbox contains whitespace or a control character", ErrHeaderInjection)
		}
	}
	if strings.ContainsAny(address, "<>,") {
		return fmt.Errorf("%w: display names and address lists are not allowed here", ErrInvalidAddress)
	}

	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	if parsed.Name != "" || parsed.Address != address {
		return fmt.Errorf("%w: expected a bare addr-spec", ErrInvalidAddress)
	}

	at := strings.LastIndexByte(address, '@')
	if at <= 0 || at == len(address)-1 {
		return fmt.Errorf("%w: mailbox must contain a local part and domain", ErrInvalidAddress)
	}
	local, domain := address[:at], address[at+1:]
	if len(local) > 64 || len(domain) > 255 {
		return fmt.Errorf("%w: local part or domain is too long", ErrInvalidAddress)
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") ||
		strings.Contains(local, "..") || strings.Contains(domain, "..") {
		return fmt.Errorf("%w: empty dot-atom segment", ErrInvalidAddress)
	}
	return nil
}

func validateText(value, field string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrHeaderInjection, field)
	}
	for _, r := range value {
		if r == '\r' || r == '\n' || unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a line break or control character", ErrHeaderInjection, field)
		}
	}
	return nil
}

var encodedWordPattern = regexp.MustCompile(`(?i)=\?[^?\s]+\?[bq]\?[^?]*\?=`)

func rejectPreEncoded(value string) error {
	if encodedWordPattern.MatchString(value) {
		return fmt.Errorf("%w: supply the decoded display text instead", ErrEncodedInput)
	}
	return nil
}
