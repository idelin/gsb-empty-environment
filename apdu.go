package netsec

const (
	INSExternalAuthenticate = 0x84

	// INS values for the GlobalPlatform card-content management commands
	// that must never be accepted on a partially protected channel.
	INSInstall = 0xE6
	INSLoad    = 0xE8
	INSDelete  = 0xE4
)

// RequiresDualProtection reports whether a command instruction requires both
// C-MAC and C-DECRYPTION.
func RequiresDualProtection(ins byte) bool {
	switch ins {
	case INSInstall, INSLoad, INSDelete:
		return true
	default:
		return false
	}
}

func commandINS(apdu []byte) (byte, error) {
	if len(apdu) < 4 {
		return 0, ErrMalformedAPDU
	}
	return apdu[1], nil
}
