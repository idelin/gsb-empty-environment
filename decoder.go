package netsec

// Decoder decodes and validates hostnames using a Profile.
type Decoder struct {
	profile *Profile
}

// NewDecoder creates a Decoder. With no options it uses registration
// validation, including the ContextJ rules.
func NewDecoder(opts ...Option) *Decoder {
	return &Decoder{profile: New(opts...)}
}

// Decode decodes and validates a hostname.
func (d *Decoder) Decode(host string) (string, error) {
	return d.profile.Decode(host)
}

// DecodeHostname decodes and validates a hostname.
func (d *Decoder) DecodeHostname(host string) (string, error) {
	return d.profile.DecodeHostname(host)
}

// DecodeLabel decodes and validates a single label.
func (d *Decoder) DecodeLabel(label string) (string, error) {
	return d.profile.DecodeLabel(label)
}
