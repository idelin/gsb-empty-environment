package sourceaddr

import (
	"crypto/sha512"
	"errors"
	"net/netip"
)

var (
	ErrInvalidPublicKey     = errors.New("sourceaddr: public key must be 32 bytes")
	ErrUnsupportedPublicKey = errors.New("sourceaddr: hash does not contain enough bits after the leading ones")
	ErrInvalidAnnounce      = errors.New("sourceaddr: announced address does not match public key")
	ErrUnauthorizedSrc      = errors.New("sourceaddr: inner source address is not authorized for peer")
)

// DeriveAddress returns the 16-byte node address derived from a 32-byte key.
func DeriveAddress(publicKey []byte) (netip.Addr, error) {
	n, suffix, err := derivedParts(publicKey)
	if err != nil {
		return netip.Addr{}, err
	}

	return addressFromParts(n, suffix), nil
}

// DerivePrefix returns the peer's /64 subnet derived from a 32-byte key.
func DerivePrefix(publicKey []byte) (netip.Prefix, error) {
	n, suffix, err := derivedParts(publicKey)
	if err != nil {
		return netip.Prefix{}, err
	}

	return makePrefix(n, suffix)
}

// CheckAnnouncedAddress accepts only the exact derived node address.
func CheckAnnouncedAddress(publicKey []byte, announced netip.Addr) error {
	derived, err := DeriveAddress(publicKey)
	if err != nil {
		return err
	}

	if !announced.IsValid() || announced != derived {
		return ErrInvalidAnnounce
	}
	return nil
}

// CheckInnerSource accepts an inner IPv6 source if it is the peer's node
// address or belongs to the peer's /64. It does not make forwarding decisions.
func CheckInnerSource(publicKey []byte, source netip.Addr) error {
	n, suffix, err := derivedParts(publicKey)
	if err != nil {
		return err
	}

	derived := addressFromParts(n, suffix)
	prefix, err := makePrefix(n, suffix)
	if err != nil {
		return err
	}

	if !source.IsValid() || source.Is4() || source.Is4In6() ||
		(source != derived && !prefix.Contains(source)) {
		return ErrUnauthorizedSrc
	}
	return nil
}

func AllowInnerSource(publicKey []byte, source netip.Addr) bool {
	return CheckInnerSource(publicKey, source) == nil
}

func addressFromParts(n int, suffix [14]byte) netip.Addr {
	var address [16]byte
	address[0] = 0x02
	address[1] = byte(n)
	copy(address[2:], suffix[:])
	return netip.AddrFrom16(address).Unmap()
}

func makePrefix(n int, suffix [14]byte) (netip.Prefix, error) {
	var prefix [16]byte
	prefix[0] = 0x03
	prefix[1] = byte(n)
	copy(prefix[2:8], suffix[:6])
	return netip.AddrFrom16(prefix).Unmap().Prefix(64)
}

func derivedParts(publicKey []byte) (int, [14]byte, error) {
	if len(publicKey) != 32 {
		return 0, [14]byte{}, ErrInvalidPublicKey
	}

	hash := sha512.Sum512(publicKey)
	return partsFromHash(hash[:])
}

func partsFromHash(hash []byte) (int, [14]byte, error) {
	n := leadingOnes(hash)
	start := n + 1
	if n > 255 || start+14*8 > len(hash)*8 {
		return 0, [14]byte{}, ErrUnsupportedPublicKey
	}

	var suffix [14]byte
	copy(suffix[:], readBits(hash, start, 14))
	return n, suffix, nil
}

func leadingOnes(input []byte) int {
	for index, value := range input {
		if value == 0xff {
			continue
		}

		for bit := 7; bit >= 0; bit-- {
			if value&(1<<bit) == 0 {
				return index*8 + (7 - bit)
			}
		}
	}
	return len(input) * 8
}

func readBits(input []byte, offset, length int) []byte {
	result := make([]byte, length)
	for i := 0; i < length*8; i++ {
		position := offset + i
		if input[position/8]&(1<<(7-position%8)) != 0 {
			result[i/8] |= 1 << (7 - i%8)
		}
	}
	return result
}
