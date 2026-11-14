package netsec

import "errors"

var (
	// Configuration and input errors.
	ErrNotConfigured       = errors.New("netsec: welcome admitter is not configured")
	ErrInvalidWelcome      = errors.New("netsec: invalid welcome")
	ErrInvalidGroupInfo    = errors.New("netsec: invalid group info")
	ErrInvalidKeyPackage   = errors.New("netsec: invalid key package")
	ErrInvalidGroupSecrets = errors.New("netsec: invalid group secrets")
	ErrInvalidRatchetTree  = errors.New("netsec: invalid ratchet tree")

	// Key package admission errors.
	ErrKeyPackageExpired      = errors.New("netsec: key package expired")
	ErrKeyPackageNotYetValid  = errors.New("netsec: key package is not yet valid")
	ErrKeyPackageConsumed     = errors.New("netsec: key package already consumed")
	ErrKeyPackageCredential   = errors.New("netsec: key package credential does not match local private key")
	ErrKeyPackageLeafMismatch = errors.New("netsec: key package leaf does not match ratchet tree")

	// Welcome binding and candidate errors.
	ErrNoMatchingSecret    = errors.New("netsec: no encrypted group secret for key package")
	ErrSecretDecryption    = errors.New("netsec: cannot decrypt encrypted group secrets")
	ErrAmbiguousCandidate  = errors.New("netsec: multiple encrypted group secrets match one key package")
	ErrMixedCandidate      = errors.New("netsec: encrypted group secrets disagree on group state")
	ErrMissingPSK          = errors.New("netsec: required pre-shared key is unavailable")
	ErrGroupMismatch       = errors.New("netsec: group id mismatch")
	ErrEpochMismatch       = errors.New("netsec: epoch mismatch")
	ErrEpochRollback       = errors.New("netsec: welcome epoch would roll back group state")
	ErrCipherSuiteMismatch = errors.New("netsec: cipher suite mismatch")
	ErrJoinerSecret        = errors.New("netsec: joiner secret does not bind group state")

	// GroupInfo, tree and transcript errors.
	ErrGroupInfoSignature     = errors.New("netsec: invalid group info signature")
	ErrSignerNotMember        = errors.New("netsec: group info signer is not in ratchet tree")
	ErrSignerNotAuthorized    = errors.New("netsec: group info signer has no signing permission")
	ErrTreeHashMismatch       = errors.New("netsec: ratchet tree hash mismatch")
	ErrTreeDecode             = errors.New("netsec: cannot decode ratchet tree")
	ErrTreeSource             = errors.New("netsec: welcome must carry exactly one ratchet tree source")
	ErrTreeFetch              = errors.New("netsec: cannot fetch external ratchet tree")
	ErrTreeBinding            = errors.New("netsec: external ratchet tree is not bound to this welcome")
	ErrTranscriptHashMismatch = errors.New("netsec: transcript hash mismatch")
	ErrExtensionsMismatch     = errors.New("netsec: group info extensions do not match ratchet tree")

	// Persistence errors.
	ErrWelcomeAlreadyConsumed = errors.New("netsec: welcome already consumed")
	ErrStateConflict          = errors.New("netsec: group state changed during admission")
)
