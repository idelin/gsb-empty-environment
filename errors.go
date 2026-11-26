package apkverify

import "errors"

var (
	ErrNotAPK                = errors.New("apkverify: not an APK")
	ErrMalformedZIP          = errors.New("apkverify: malformed ZIP")
	ErrZIP64                 = errors.New("apkverify: ZIP64 is not allowed")
	ErrEOCD                  = errors.New("apkverify: invalid end of central directory")
	ErrEntryMismatch         = errors.New("apkverify: local header and central directory mismatch")
	ErrDuplicateEntry        = errors.New("apkverify: duplicate entry name")
	ErrOverlappingEntry      = errors.New("apkverify: overlapping local entries")
	ErrSigningBlock          = errors.New("apkverify: invalid APK signing block")
	ErrSignaturePolicy       = errors.New("apkverify: signature policy rejected APK")
	ErrSignatureVerification = errors.New("apkverify: signature verification failed")
	ErrVerifierRequired      = errors.New("apkverify: signature verifier is required")
	ErrV1Only                = errors.New("apkverify: APK has no v2 or newer signature block")
)
