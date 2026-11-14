package netsec

// EncodeAddress validates and encodes a raw display name and bare ASCII
// addr-spec using RFC 5322 quoting and RFC 2047 encoded-words as needed.
func EncodeAddress(name, address string) (string, error) {
	value, err := NewAddress(name, address)
	if err != nil {
		return "", err
	}
	return value.Encode()
}

// ValidateAddress validates a bare ASCII addr-spec. Display names and address
// lists are intentionally rejected.
func ValidateAddress(address string) error {
	return validateMailbox(address)
}

// ValidateEnvelope validates an SMTP reverse-path and recipient list using the
// same rules as NewEnvelope.
func ValidateEnvelope(from string, recipients []string) error {
	_, err := NewEnvelope(from, recipients)
	return err
}
