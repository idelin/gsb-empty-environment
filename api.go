package projection

// SynthesizeProjection is an explicit-name alias for Synthesize.
func SynthesizeProjection(protocol Protocol, config Config) (Result, error) {
	return Synthesize(protocol, config)
}

// Verify is a short alias for VerifyCertificate.
func Verify(protocol Protocol, config Config, certificate Certificate) error {
	return VerifyCertificate(protocol, config, certificate)
}

// CheckProjection is an alias for CheckSafety.
func CheckProjection(protocol Protocol, projection Projection) (*SafetyCertificate, *SafetyFailure, error) {
	return CheckSafety(protocol, projection)
}
