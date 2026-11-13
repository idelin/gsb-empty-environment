package netsec

import "errors"

// Sentinel errors returned by transaction decoding and admission checks.
var (
	ErrMalformedTx = errors.New("malformed transaction")

	ErrExtensionOptions = errors.New("transaction body extension_options must be empty")

	ErrCriticalUnknownField = errors.New("unknown critical transaction field")

	ErrNonCriticalUnknownField = errors.New(
		"unknown non-critical transaction field is not allowed for this sign mode",
	)

	ErrNonCriticalExtensionOptions = errors.New(
		"non-critical extension options are not allowed for this sign mode",
	)

	ErrAminoMessageNotRegistered = errors.New(
		"message type has no amino name in the caller registry",
	)

	ErrLegacyAminoJSONDeprecated = errors.New(
		"SIGN_MODE_LEGACY_AMINO_JSON is no longer allowed at this height",
	)

	// ErrNoLegacyAminoSigner is returned when the deprecation gate is active
	// but the caller did not supply any signer addresses.
	ErrNoLegacyAminoSigner = errors.New("legacy Amino JSON signer addresses are unavailable")

	// Convenience aliases used by some integrations.
	ErrExtensionOptionsNonEmpty          = ErrExtensionOptions
	ErrUnknownCriticalField              = ErrCriticalUnknownField
	ErrUnknownNonCriticalField           = ErrNonCriticalUnknownField
	ErrUnregisteredMessage               = ErrAminoMessageNotRegistered
	ErrLegacyAminoJSONDeprecatedAtHeight = ErrLegacyAminoJSONDeprecated
)
