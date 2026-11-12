package netsec

import "errors"

var (
	ErrMalformedMetadata  = errors.New("sops metadata is malformed")
	ErrUnsupportedEntry   = errors.New("sops key group contains an unsupported entry type")
	ErrInvalidPolicy      = errors.New("sops validation policy is invalid")
	ErrKeyGroupsMismatch  = errors.New("sops key groups do not match the expected key groups")
	ErrThresholdViolation = errors.New("sops shamir threshold violates policy")
	ErrMissingMAC         = errors.New("sops metadata has no mac")
	ErrVersionViolation   = errors.New("sops version is below the minimum version")
	ErrInvalidVersion     = errors.New("sops version is invalid")
	ErrMACMismatch        = errors.New("sops mac does not match the decrypted values")
)
