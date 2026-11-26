package nsec3

import "errors"

// Sentinel errors returned by the verifier. All errors returned by this
// package wrap one of these values, so callers can use errors.Is.
var (
	// ErrInvalidName is returned for malformed presentation-format names.
	ErrInvalidName = errors.New("nsec3: invalid domain name")

	// ErrInvalidRecord is returned for a malformed NSEC3 record.
	ErrInvalidRecord = errors.New("nsec3: invalid NSEC3 record")

	// ErrUnknownAlgorithm is returned when the hash algorithm is not
	// algorithm 1 (SHA-1), the only algorithm defined by RFC 5155.
	ErrUnknownAlgorithm = errors.New("nsec3: unknown hash algorithm")

	// ErrIterationsTooLarge is returned when the iterations field
	// exceeds MaxIterations.
	ErrIterationsTooLarge = errors.New("nsec3: iterations exceed maximum")

	// ErrParamMismatch is returned when records presented as one proof
	// do not share the same NSEC3 parameters (algorithm, iterations,
	// salt), which indicates records from different zones.
	ErrParamMismatch = errors.New("nsec3: mismatched NSEC3 parameters across records")

	// ErrNoRecords is returned when a proof is checked against an empty
	// record set.
	ErrNoRecords = errors.New("nsec3: no NSEC3 records supplied")

	// ErrDuplicateOwner is returned when two records share an owner hash.
	ErrDuplicateOwner = errors.New("nsec3: duplicate NSEC3 owner hash")

	// ErrNoClosestEncloser means no ancestor of the QNAME has an exact
	// matching NSEC3 owner.
	ErrNoClosestEncloser = errors.New("nsec3: no closest encloser proof")

	// ErrNextCloserNotCovered means no NSEC3 record covers the next
	// closer name hash.
	ErrNextCloserNotCovered = errors.New("nsec3: next closer name not covered")

	// ErrWildcardNotCovered means no NSEC3 record covers the hash of
	// "*.<closest encloser>".
	ErrWildcardNotCovered = errors.New("nsec3: wildcard name not covered")

	// ErrNoExactMatch means no NSEC3 record matches the QNAME hash.
	ErrNoExactMatch = errors.New("nsec3: no exact matching NSEC3 record")

	// ErrTypePresent means the requested type bit is set in the exact
	// matching NSEC3 type bitmap, so NODATA cannot be proven.
	ErrTypePresent = errors.New("nsec3: qtype present in type bitmap")

	// ErrCNAMEPresent means a CNAME bit is set, which contradicts a
	// NODATA denial.
	ErrCNAMEPresent = errors.New("nsec3: CNAME present in type bitmap")

	// ErrSOAPresent means a DS query hit an exact match whose bitmap
	// contains SOA (a zone apex); DS absence must be proven via the
	// delegation / Opt-Out path instead.
	ErrSOAPresent = errors.New("nsec3: SOA present at DS query apex")

	// ErrOptOutRequired means a covering record used to prove an
	// unsigned delegation does not have the Opt-Out flag set.
	ErrOptOutRequired = errors.New("nsec3: covering record lacks Opt-Out flag")

	// ErrOptOutNotApplicable means an Opt-Out covering record was used
	// for a query type other than DS, where Opt-Out proves nothing.
	ErrOptOutNotApplicable = errors.New("nsec3: Opt-Out proof only valid for DS queries")

	// ErrDelegationPoint means the closest encloser itself carries an NS
	// bit, so it is a delegation point and cannot anchor an unsigned
	// delegation proof for a name below it.
	ErrDelegationPoint = errors.New("nsec3: closest encloser is a delegation point")

	// ErrBadBase32Hex is returned for malformed base32hex input.
	ErrBadBase32Hex = errors.New("nsec3: invalid base32hex encoding")
)
