package netsec

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type options struct {
	transitional      bool
	useSTD3Rules      bool
	checkHyphens      bool
	checkJoiners      bool
	verifyDNSLength   bool
	removeLeadingDots bool
}

// Option configures a Profile.
type Option func(*options)

// Transitional selects the UTS #46 transitional mapping. The mapping is kept
// for API compatibility; this implementation does not remove deviation
// characters.
func Transitional(enabled bool) Option {
	return func(o *options) { o.transitional = enabled }
}

// VerifyDNSLength enables the DNS label and domain length checks.
func VerifyDNSLength(enabled bool) Option {
	return func(o *options) { o.verifyDNSLength = enabled }
}

// RemoveLeadingDots removes leading empty labels.
func RemoveLeadingDots(enabled bool) Option {
	return func(o *options) { o.removeLeadingDots = enabled }
}

// ValidateLabels enables label validation, including the ContextJ rules.
func ValidateLabels(enabled bool) Option {
	return func(o *options) {
		o.checkHyphens = enabled
		o.checkJoiners = enabled
	}
}

// CheckHyphens enables the hyphen restrictions from Section 4.1 of RFC 5891.
func CheckHyphens(enabled bool) Option {
	return func(o *options) { o.checkHyphens = enabled }
}

// CheckJoiners enables the ContextJ rules for ZWNJ and ZWJ.
func CheckJoiners(enabled bool) Option {
	return func(o *options) { o.checkJoiners = enabled }
}

// StrictDomainName restricts ASCII characters to letters, digits, and hyphen.
func StrictDomainName(enabled bool) Option {
	return func(o *options) { o.useSTD3Rules = enabled }
}

// BidiRule is accepted for API compatibility. Bidi validation is outside the
// scope of the ContextJ checks implemented here.
func BidiRule() Option {
	return func(o *options) {}
}

// ValidateForRegistration configures a Profile for IDN registration.
func ValidateForRegistration() Option {
	return func(o *options) {
		o.useSTD3Rules = true
		o.checkHyphens = true
		o.checkJoiners = true
		o.verifyDNSLength = true
	}
}

// MapForLookup configures a Profile for hostname lookup.
func MapForLookup() Option {
	return func(o *options) {
		o.useSTD3Rules = true
		o.checkHyphens = true
		o.checkJoiners = true
	}
}

// Profile is an IDNA processing configuration.
type Profile struct {
	options
}

// New creates a Profile. Without options it validates labels, including the
// ContextJ rules.
func New(opts ...Option) *Profile {
	p := &Profile{options: options{
		useSTD3Rules: true,
		checkHyphens: true,
		checkJoiners: true,
	}}
	for _, opt := range opts {
		opt(&p.options)
	}
	return p
}

var (
	// Lookup is the recommended profile for hostname lookup.
	Lookup = New(MapForLookup())

	// Display is the recommended profile for displaying hostnames.
	Display = New(MapForLookup())

	// Registration is the recommended profile for IDN registration.
	Registration = New(ValidateForRegistration())

	// Punycode performs raw Punycode conversion without ContextJ validation.
	Punycode = New(CheckJoiners(false), CheckHyphens(false), StrictDomainName(false))
)

// ToASCII converts a domain name to its ASCII form.
func ToASCII(s string) (string, error) {
	return Lookup.ToASCII(s)
}

// ToUnicode converts a domain name to its Unicode form.
func ToUnicode(s string) (string, error) {
	return Display.ToUnicode(s)
}

// Decode decodes and validates a hostname.
func Decode(s string) (string, error) {
	return Registration.ToUnicode(s)
}

// DecodeHostname decodes and validates a hostname.
func DecodeHostname(s string) (string, error) {
	return Decode(s)
}

// Hostname decodes and validates a hostname.
func Hostname(s string) (string, error) {
	return Decode(s)
}

// Validate checks whether s is a valid hostname for registration.
func Validate(s string) error {
	return Registration.Validate(s)
}

// ValidateHostname checks whether s is a valid hostname for registration.
func ValidateHostname(s string) error {
	return Validate(s)
}

// ValidateLabel checks whether label satisfies the registration rules and the
// ContextJ rules.
func ValidateLabel(label string) error {
	return Registration.ValidateLabel(label)
}

// IsValid reports whether s is a valid hostname for registration.
func IsValid(s string) bool {
	return Validate(s) == nil
}

// IsValidHostname reports whether s is a valid hostname for registration.
func IsValidHostname(s string) bool {
	return IsValid(s)
}

// IsValidLabel reports whether label is valid for registration.
func IsValidLabel(label string) bool {
	return ValidateLabel(label) == nil
}

// ToASCII converts a domain name to its ASCII form.
func (p *Profile) ToASCII(s string) (string, error) {
	return p.process(s, true)
}

// ToUnicode converts a domain name to its Unicode form.
func (p *Profile) ToUnicode(s string) (string, error) {
	return p.process(s, false)
}

// Decode decodes and validates a hostname.
func (p *Profile) Decode(s string) (string, error) {
	return p.process(s, false)
}

// DecodeHostname decodes and validates a hostname.
func (p *Profile) DecodeHostname(s string) (string, error) {
	return p.process(s, false)
}

// Hostname decodes and validates a hostname.
func (p *Profile) Hostname(s string) (string, error) {
	return p.process(s, false)
}

// Validate checks a hostname.
func (p *Profile) Validate(s string) error {
	_, err := p.process(s, false)
	return err
}

// ValidateHostname checks a hostname.
func (p *Profile) ValidateHostname(s string) error {
	return p.Validate(s)
}

// ValidateLabel checks a single label.
func (p *Profile) ValidateLabel(label string) error {
	if strings.HasPrefix(strings.ToLower(label), acePrefix) {
		body := label[len(acePrefix):]
		if body == "" {
			return &PunycodeError{Label: label}
		}
		decoded, err := punycodeDecode(body)
		if err != nil {
			return err
		}
		return p.validateLabel(decoded)
	}
	return p.validateLabel(label)
}

// CheckJoiners verifies the ContextJ rules for a single label.
func (p *Profile) CheckJoiners(label string) error {
	return checkJoiners(label)
}

// DecodeLabel decodes a single A-label or validates a U-label.
func (p *Profile) DecodeLabel(label string) (string, error) {
	if strings.HasPrefix(strings.ToLower(label), acePrefix) {
		body := label[len(acePrefix):]
		if body == "" {
			return label, &PunycodeError{Label: label}
		}
		decoded, err := punycodeDecode(body)
		if err != nil {
			return label, err
		}
		if err := p.validateLabel(decoded); err != nil {
			return decoded, err
		}
		return decoded, nil
	}
	mapped := p.mapLabel(label)
	if err := p.validateLabel(mapped); err != nil {
		return mapped, err
	}
	return mapped, nil
}

// EncodeLabel validates a single label and returns its A-label form.
func (p *Profile) EncodeLabel(label string) (string, error) {
	mapped := p.mapLabel(label)
	if err := p.validateLabel(mapped); err != nil {
		return mapped, err
	}
	if isASCII(mapped) {
		return mapped, nil
	}
	return punycodeEncode(acePrefix, mapped)
}

// DecodeLabel decodes a single A-label or validates a U-label.
func DecodeLabel(label string) (string, error) {
	return Registration.DecodeLabel(label)
}

// EncodeLabel validates a single label and returns its A-label form.
func EncodeLabel(label string) (string, error) {
	return Registration.EncodeLabel(label)
}

func (p *Profile) process(s string, toASCII bool) (string, error) {
	if !utf8.ValidString(s) {
		return s, &LabelError{Label: s, Code: "P1"}
	}
	s = mapDotVariants(s)
	if p.removeLeadingDots {
		for len(s) > 0 && s[0] == '.' {
			s = s[1:]
		}
	}

	var firstErr error
	if p.verifyDNSLength && s == "" {
		firstErr = &LabelError{Label: s, Code: "A4"}
	}

	labels := strings.Split(s, ".")
	for i, label := range labels {
		if label == "" {
			if p.verifyDNSLength && i != len(labels)-1 && firstErr == nil {
				firstErr = &LabelError{Label: s, Code: "A4"}
			}
			continue
		}
		mapped := p.mapLabel(label)
		if strings.HasPrefix(strings.ToLower(mapped), acePrefix) {
			body := mapped[len(acePrefix):]
			if body == "" {
				if firstErr == nil {
					firstErr = &PunycodeError{Label: mapped}
				}
				continue
			}
			decoded, err := punycodeDecode(body)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := p.validateLabel(decoded); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
			if toASCII {
				if isASCII(decoded) {
					labels[i] = strings.ToLower(decoded)
				} else {
					encoded, err := punycodeEncode(acePrefix, decoded)
					if err != nil {
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					labels[i] = encoded
				}
			} else {
				labels[i] = decoded
			}
			continue
		}

		if err := p.validateLabel(mapped); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		if toASCII {
			if isASCII(mapped) {
				labels[i] = strings.ToLower(mapped)
			} else {
				encoded, err := punycodeEncode(acePrefix, mapped)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				labels[i] = encoded
			}
		} else {
			labels[i] = mapped
		}
	}

	result := strings.Join(labels, ".")
	if p.verifyDNSLength && firstErr == nil {
		for _, label := range labels {
			if label == "" {
				continue
			}
			if len(label) > 63 {
				firstErr = &LabelError{Label: label, Code: "A4"}
				break
			}
		}
		n := len(result)
		if n > 0 && result[n-1] == '.' {
			n--
		}
		if n < 1 || n > 253 {
			firstErr = &LabelError{Label: result, Code: "A4"}
		}
	}
	return result, firstErr
}

func (p *Profile) mapLabel(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		default:
			return unicode.ToLower(r)
		}
	}, s)
}

func mapDotVariants(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		default:
			return r
		}
	}, s)
}

func (p *Profile) validateLabel(s string) error {
	if s == "" {
		return nil
	}
	if !utf8.ValidString(s) {
		return &LabelError{Label: s, Code: "P1"}
	}
	if p.checkHyphens {
		if len(s) > 4 && s[2] == '-' && s[3] == '-' {
			return &LabelError{Label: s, Code: "V2"}
		}
		if s[0] == '-' || s[len(s)-1] == '-' {
			return &LabelError{Label: s, Code: "V3"}
		}
	}
	if p.checkJoiners {
		if err := checkJoiners(s); err != nil {
			return err
		}
	}

	for _, r := range s {
		if r == '\u200c' || r == '\u200d' {
			continue
		}
		if r < utf8.RuneSelf {
			if r == '-' || isASCIILetter(r) || isASCIIDigit(r) {
				continue
			}
			if !p.useSTD3Rules && (r == '_' || r == '~') {
				continue
			}
			return &LabelError{Label: s, Code: "P1"}
		}
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r) && !unicode.IsNumber(r) {
			return &LabelError{Label: s, Code: "P1"}
		}
	}
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isASCIILetter(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
}

func isASCIIDigit(r rune) bool {
	return '0' <= r && r <= '9'
}
