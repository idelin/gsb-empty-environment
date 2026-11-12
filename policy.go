package netsec

// Status is the policy verdict for a delegation point being validated.
type Status int

const (
	// StatusInsecure means no usable (non-disabled) algorithm survived the
	// policy. Per the problem rules the zone is treated as insecure rather
	// than as a hard validation failure.
	StatusInsecure Status = iota

	// StatusSecure means the strongest usable algorithm's DNSKEY built a
	// chain that verified successfully.
	StatusSecure

	// StatusBogus means at least one usable algorithm existed, but the
	// strongest one could not be verified (no acceptable DNSKEY or a failed
	// chain). A weaker algorithm verifying never overrides this result.
	StatusBogus
)

// String returns a stable textual name for the status.
func (s Status) String() string {
	switch s {
	case StatusSecure:
		return "secure"
	case StatusBogus:
		return "bogus"
	default:
		return "insecure"
	}
}

// Machine-readable reason codes attached to a Decision.
const (
	// ReasonNoUsableAlgorithm: every algorithm advertised by DS is disabled.
	ReasonNoUsableAlgorithm = "no-usable-algorithm"
	// ReasonNoAcceptableDNSKEY: the strongest algorithm has no DNSKEY that
	// passes the strength gate (short or unparseable RSA key).
	ReasonNoAcceptableDNSKEY = "no-acceptable-dnskey"
	// ReasonNoVerifier: no chain verification callback was supplied.
	ReasonNoVerifier = "no-verifier"
	// ReasonChainFailed: every acceptable strongest-algorithm DNSKEY failed
	// chain verification; the callback error is quoted on the decision.
	ReasonChainFailed = "chain-failed"
	// ReasonSecure: the strongest algorithm verified successfully.
	ReasonSecure = "secure"
)

// DSRecord is the subset of a DS record the policy needs. Algorithm is the
// DNSKEY algorithm number (DS RDATA field) that the parent delegates trust
// to; KeyTag identifies the referenced DNSKEY.
type DSRecord struct {
	KeyTag    uint16
	Algorithm uint8
}

// DNSKeyRecord is the subset of a DNSKEY record the policy needs. PublicKey
// is the base64-decoded public key blob from the RDATA.
type DNSKeyRecord struct {
	KeyTag    uint16
	Algorithm uint8
	PublicKey []byte
}

// ChainVerifier performs the cryptographic chain validation for a single
// candidate DNSKEY against the DS records that reference it. It is supplied
// by the chain-validation stage so the policy stays free of crypto logic.
// Returning nil means the chain verifies; a non-nil error means it does not.
type ChainVerifier func(ds DSRecord, key DNSKeyRecord) error

// Decision is the result of evaluating the algorithm policy at a delegation
// point.
type Decision struct {
	Status    Status
	Reason    string
	Algorithm uint8 // strongest surviving algorithm; zero when insecure
	Err       error // last chain error, only for StatusBogus
}

// Policy captures the validation-time algorithm and strength constraints.
//
// DisabledAlgorithms supplements the built-in set (RSAMD5, both DSA
// variants) with caller-provided algorithm numbers that must be treated as
// if they were never advertised. MinRSABits is the floor below which an RSA
// DNSKEY modulus makes the key unusable. A value of zero disables the RSA
// length gate.
type Policy struct {
	DisabledAlgorithms []uint8
	MinRSABits         int
}

// defaultDisabledAlgorithms are weak numbers the policy always rejects.
// RSAMD5 (1) and the DSA family (DSA 3 and DSA-NSEC3-SHA1 6) are treated as
// non-existent.
var defaultDisabledAlgorithms = map[uint8]bool{
	AlgorithmRSAMD5:       true,
	AlgorithmDSA:          true,
	AlgorithmDSANSEC3SHA1: true,
}

// disabled returns a lookup of every algorithm number the policy rejects.
func (p Policy) disabled() map[uint8]bool {
	disabled := make(map[uint8]bool, len(defaultDisabledAlgorithms)+len(p.DisabledAlgorithms))
	for algorithm := range defaultDisabledAlgorithms {
		disabled[algorithm] = true
	}
	for _, algorithm := range p.DisabledAlgorithms {
		disabled[algorithm] = true
	}
	return disabled
}

// IsDisabled reports whether the policy treats algorithm as non-existent.
func (p Policy) IsDisabled(algorithm uint8) bool {
	return p.disabled()[algorithm]
}

// keyAcceptable reports whether a DNSKEY may be used for the strongest
// algorithm. Non-RSA keys are only gated on algorithm policy (already
// applied); RSA keys must additionally meet the minimum modulus length.
func (p Policy) keyAcceptable(key DNSKeyRecord) bool {
	if !isRSAAlgorithm(key.Algorithm) {
		return true
	}
	if p.MinRSABits <= 0 {
		return true
	}
	bits, ok := rsaModulusBits(key.PublicKey)
	if !ok {
		return false
	}
	return bits >= p.MinRSABits
}

// Evaluate applies the downgrade-resistant algorithm policy.
//
// ds is the set of DS records at the delegation point and keys is the
// child zone's DNSKEY RRset. verify is invoked only for the strongest
// surviving algorithm; weaker algorithms are never consulted, so a weak
// chain cannot rescue a missing or failed strong chain.
func (p Policy) Evaluate(ds []DSRecord, keys []DNSKeyRecord, verify ChainVerifier) Decision {
	disabled := p.disabled()

	// Collect the distinct algorithms advertised by DS that survive policy.
	usable := make(map[uint8]bool)
	for _, record := range ds {
		if !disabled[record.Algorithm] {
			usable[record.Algorithm] = true
		}
	}

	if len(usable) == 0 {
		// Nothing the policy trusts remains: insecure zone, not a failure.
		return Decision{Status: StatusInsecure, Reason: ReasonNoUsableAlgorithm}
	}

	strongest := uint8(0)
	for algorithm := range usable {
		if algorithm > strongest {
			strongest = algorithm
		}
	}

	// Restrict to acceptable DNSKEYs for the strongest algorithm only.
	var candidates []DNSKeyRecord
	for _, key := range keys {
		if key.Algorithm == strongest && p.keyAcceptable(key) {
			candidates = append(candidates, key)
		}
	}

	if len(candidates) == 0 {
		return Decision{
			Status:    StatusBogus,
			Reason:    ReasonNoAcceptableDNSKEY,
			Algorithm: strongest,
		}
	}

	if verify == nil {
		return Decision{
			Status:    StatusBogus,
			Reason:    ReasonNoVerifier,
			Algorithm: strongest,
		}
	}

	// Map DS records by key tag for the strongest algorithm.
	dsByTag := make(map[uint16]DSRecord)
	for _, record := range ds {
		if record.Algorithm == strongest {
			dsByTag[record.KeyTag] = record
		}
	}

	var lastErr error
	for _, key := range candidates {
		record, ok := dsByTag[key.KeyTag]
		if !ok {
			// No DS delegates trust to this DNSKEY; try the next candidate.
			continue
		}
		if err := verify(record, key); err != nil {
			lastErr = err
			continue
		}
		return Decision{
			Status:    StatusSecure,
			Reason:    ReasonSecure,
			Algorithm: strongest,
		}
	}

	return Decision{
		Status:    StatusBogus,
		Reason:    ReasonChainFailed,
		Algorithm: strongest,
		Err:       lastErr,
	}
}
