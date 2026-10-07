package encobj

import "errors"

var (
	ErrNotExist          = errors.New("encobj: not exist")
	ErrLocked            = errors.New("encobj: store is locked by another writer")
	ErrObjectNotFound    = errors.New("encobj: object not found")
	ErrConflict          = errors.New("encobj: idempotency conflict: same op ID with different content")
	ErrNonceExhausted    = errors.New("encobj: nonce counter limit reached")
	ErrCorrupt           = errors.New("encobj: corrupt persistent state")
	ErrAuth              = errors.New("encobj: authentication failed")
	ErrKeyExists         = errors.New("encobj: key ID already registered with different material")
	ErrKeyMaterialReused = errors.New("encobj: same key material registered under a different key ID")
	ErrKeyNotFound       = errors.New("encobj: unknown key ID")
	ErrKeyRetired        = errors.New("encobj: key ID has been retired")
	ErrKeyInUse          = errors.New("encobj: key ID still has live references or is active")
	ErrCrashed           = errors.New("encobj: store has crashed (fault injection)")
)
