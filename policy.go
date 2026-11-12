package netsec

// EncryptionMode identifies an fscrypt encryption mode for file contents or
// file names.
type EncryptionMode uint8

const (
	ModeInvalid EncryptionMode = iota
	ModeAES256XTS
	ModeAES256CTS
	ModeAdiantum
)

// Policy flags, mirroring FSCRYPT_POLICY_FLAGS_*.
const (
	FlagDirectKey   uint8 = 0x01
	FlagIVInoLblk64 uint8 = 0x02
	FlagIVInoLblk32 uint8 = 0x04
	FlagPad16       uint8 = 0x10
	FlagPad32       uint8 = 0x20
)

// Policy describes an fscrypt encryption policy. The zero value is not a
// valid policy; a nil *Policy represents "no encryption" (plaintext).
type Policy struct {
	Version             uint8
	MasterKeyIdentifier string
	ContentsMode        EncryptionMode
	FilenamesMode       EncryptionMode
	Flags               uint8
}

// Equal reports whether two policies are identical in every field. Two
// directories may exchange entries via rename/link only if their policies
// are Equal (or both absent).
func (p *Policy) Equal(other *Policy) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	return *p == *other
}

func (p *Policy) clone() *Policy {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
