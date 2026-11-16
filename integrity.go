package netsec

import (
	"crypto/aes"
	"crypto/subtle"
	"errors"
)

// NAS integrity algorithm identifiers (TS 24.501 section 9.11.3.8, field
// "type of integrity protection algorithm").
const (
	NIA0 byte = 0 // 5G-IA0: null integrity protection
	NIA1 byte = 1 // 128-NIA1: SNOW 3G
	NIA2 byte = 2 // 128-NIA2: AES-CMAC
	NIA3 byte = 3 // 128-NIA3: ZUC
)

// nasBearer is the BEARER parameter for NAS signalling (TS 33.501 Annex B.2).
const nasBearer byte = 0x02

// Direction distinguishes uplink (UE -> network) and downlink messages for
// NAS-MAC computation (TS 33.501 Annex B.1.2).
type Direction uint8

const (
	// DirectionDownlink covers messages sent by the network to the UE.
	DirectionDownlink Direction = 0
	// DirectionUplink covers messages sent by the UE to the network.
	DirectionUplink Direction = 1
)

func (d Direction) bit() byte {
	if d == DirectionUplink {
		return 1
	}
	return 0
}

// IntegrityAlgorithm computes a 4-octet NAS-MAC over a NAS message.
// Implementations must follow TS 33.501 Annex B / TS 33.401 section 11:
// the message M is built from COUNT (32 bit), BEARER (5 bit), direction (1
// bit), six zero padding bits and the message bit string, then the first 32
// bits of the algorithm output form the NAS-MAC.
type IntegrityAlgorithm interface {
	ID() byte
	ComputeMAC(kNasInt []byte, count uint32, direction Direction, message []byte) ([4]byte, error)
}

var integrityAlgorithms = map[byte]IntegrityAlgorithm{
	NIA0: nia0{},
	NIA2: nia2{},
}

// RegisterIntegrityAlgorithm makes an additional integrity algorithm
// (typically 128-NIA1/SNOW 3G or 128-NIA3/ZUC, which need external crypto
// implementations) available to the SMC verifier. Registering NIA0 or NIA2
// overrides the built-in implementation.
func RegisterIntegrityAlgorithm(alg IntegrityAlgorithm) {
	if alg != nil {
		integrityAlgorithms[alg.ID()] = alg
	}
}

// nia0 is the null integrity algorithm (TS 33.501 Annex B.1.1): the NAS-MAC
// field is set to zero and no verification is possible.
type nia0 struct{}

func (nia0) ID() byte { return NIA0 }

func (nia0) ComputeMAC(_ []byte, _ uint32, _ Direction, _ []byte) ([4]byte, error) {
	return [4]byte{}, nil
}

// nia2 implements 128-NIA2 (AES-CMAC) per TS 33.401 section 11.4 and
// TS 33.501 Annex B.2.3.
type nia2 struct{}

func (nia2) ID() byte { return NIA2 }

func (nia2) ComputeMAC(kNasInt []byte, count uint32, direction Direction, message []byte) ([4]byte, error) {
	if len(kNasInt) != 16 {
		return [4]byte{}, errors.New("netsec: NIA2 requires a 128-bit K_nasint")
	}
	m := buildIntegrityInput(count, direction, message)
	mac, err := aesCMAC(kNasInt, m)
	if err != nil {
		return [4]byte{}, err
	}
	var out [4]byte
	copy(out[:], mac[:4])
	return out, nil
}

// buildIntegrityInput assembles the bit string M = COUNT || BEARER ||
// DIRECTION || 0^6 || message as defined in TS 33.501 Annex B.1.
func buildIntegrityInput(count uint32, direction Direction, message []byte) []byte {
	m := make([]byte, 0, 8+len(message))
	m = append(m,
		byte(count>>24), byte(count>>16), byte(count>>8), byte(count),
		(nasBearer<<3)|(direction.bit()<<2),
		0, 0, 0,
	)
	return append(m, message...)
}

// equalMAC compares two NAS-MAC values in constant time.
func equalMAC(a, b [4]byte) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// aesCMAC computes AES-128-CMAC (RFC 4493), the primitive behind 128-NIA2.
func aesCMAC(key, msg []byte) ([16]byte, error) {
	var mac [16]byte
	block, err := aes.NewCipher(key)
	if err != nil {
		return mac, err
	}

	// Subkey derivation: L = AES(0^128); K1 = L<<1, K2 = L<<2 with the
	// Rb = 0x87 constant folded in on a carried MSB.
	var l [16]byte
	block.Encrypt(l[:], make([]byte, 16))
	k1 := cmacShift(l)
	if l[0]&0x80 != 0 {
		k1[15] ^= 0x87
	}
	k2 := cmacShift(k1)
	if k1[0]&0x80 != 0 {
		k2[15] ^= 0x87
	}

	n := len(msg) / 16
	var last [16]byte
	complete := len(msg)%16 == 0
	switch {
	case len(msg) == 0:
		n = 1
		copy(last[:], k2[:])
		last[0] ^= 0x80
	case complete:
		copy(last[:], msg[(n-1)*16:])
		for i := range last {
			last[i] ^= k1[i]
		}
	default:
		n++
		part := msg[(n-1)*16:]
		copy(last[:], part)
		last[len(part)] = 0x80
		for i := range last {
			last[i] ^= k2[i]
		}
	}

	var x [16]byte
	for i := 0; i < n-1; i++ {
		var y [16]byte
		for j := range y {
			y[j] = x[j] ^ msg[i*16+j]
		}
		block.Encrypt(x[:], y[:])
	}
	for j := range mac {
		mac[j] = x[j] ^ last[j]
	}
	block.Encrypt(mac[:], mac[:])
	return mac, nil
}

// cmacShift returns the 128-bit left shift of in (RFC 4493 subkey step); the
// Rb XOR is applied separately by the caller when the MSB was set.
func cmacShift(in [16]byte) [16]byte {
	var out [16]byte
	var carry byte
	for i := 15; i >= 0; i-- {
		out[i] = (in[i] << 1) | carry
		carry = (in[i] >> 7) & 0x01
	}
	return out
}
