package zoneinterp

import "errors"

// Sentinel errors returned by tree mutations.
var (
	ErrNotFound      = errors.New("zoneinterp: node not found")
	ErrNotDelegation = errors.New("zoneinterp: node is not a delegation")
	ErrCutCrossed    = errors.New("zoneinterp: cannot cross a delegation cut")
	ErrApexProtected = errors.New("zoneinterp: apex is protected")
)
