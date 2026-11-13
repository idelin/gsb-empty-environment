package netsec

import "errors"

var (
	ErrUnknownDevice           = errors.New("netsec: unknown DevEUI")
	ErrJoinEUIMismatch         = errors.New("netsec: JoinEUI does not match registered device")
	ErrMICMismatch             = errors.New("netsec: JoinRequest MIC verification failed")
	ErrDevNonceReused          = errors.New("netsec: DevNonce already consumed")
	ErrJoinNonceExhausted      = errors.New("netsec: JoinNonce space exhausted, device permanently rejected")
	ErrNoPendingJoin           = errors.New("netsec: no pending JoinRequest on device")
	ErrAcceptAuthFailed        = errors.New("netsec: JoinAccept authentication failed")
	ErrAcceptWrongDevice       = errors.New("netsec: JoinAccept addresses a different device")
	ErrDevNonceMismatch        = errors.New("netsec: JoinAccept DevNonce does not match pending request")
	ErrStaleJoinNonce          = errors.New("netsec: JoinNonce not greater than last accepted value")
	ErrSessionReplayed         = errors.New("netsec: sessionID already seen")
	ErrUnknownKeyHandle        = errors.New("netsec: unknown key handle")
	ErrInvalidKey              = errors.New("netsec: invalid key material")
	ErrMonotonicViolation      = errors.New("netsec: trusted counter must strictly increase")
	ErrMalformedMessage        = errors.New("netsec: malformed message")
	ErrDeviceAlreadyRegistered = errors.New("netsec: DevEUI already registered")
)
