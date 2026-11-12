package netsec

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"math/big"
	"strings"
)

// wireReader consumes fields encoded in the SSH wire format (RFC 4251):
// uint32 length prefixes precede strings, mpints and name-lists.
type wireReader struct {
	full []byte
	pos  int
	err  error
}

func (r *wireReader) remaining() int { return len(r.full) - r.pos }

func (r *wireReader) byte() byte {
	if r.err != nil || r.remaining() < 1 {
		r.err = ErrMalformedCertificate
		return 0
	}
	v := r.full[r.pos]
	r.pos++
	return v
}

func (r *wireReader) uint32() uint64 {
	if r.err != nil || r.remaining() < 4 {
		r.err = ErrMalformedCertificate
		return 0
	}
	v := binary.BigEndian.Uint32(r.full[r.pos:])
	r.pos += 4
	return uint64(v)
}

func (r *wireReader) stringBytes() []byte {
	if r.err != nil {
		return nil
	}
	n := r.uint32()
	if r.err != nil || n > uint64(r.remaining()) {
		r.err = ErrMalformedCertificate
		return nil
	}
	v := r.full[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return v
}

func (r *wireReader) string() string {
	return string(r.stringBytes())
}

func (r *wireReader) nameList() []string {
	raw := r.stringBytes()
	if r.err != nil {
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			out = append(out, string(raw[start:i]))
			start = i + 1
		}
	}
	return out
}

func writeString(dst []byte, s []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

func writeUint32(dst []byte, v uint64) []byte {
	return binary.BigEndian.AppendUint32(dst, uint32(v))
}

// mpint per RFC 4251: two's complement, minimal big-endian representation.
func writeMpint(dst []byte, v *big.Int) []byte {
	b := v.Bytes()
	if len(b) > 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return writeString(dst, b)
}

func baseKeyType(certType string) string {
	switch certType {
	case "ssh-rsa-cert-v01@openssh.com":
		return "ssh-rsa"
	case "ssh-ed25519-cert-v01@openssh.com":
		return "ssh-ed25519"
	case "ecdsa-sha2-nistp256-cert-v01@openssh.com":
		return "ecdsa-sha2-nistp256"
	case "ecdsa-sha2-nistp384-cert-v01@openssh.com":
		return "ecdsa-sha2-nistp384"
	case "ecdsa-sha2-nistp521-cert-v01@openssh.com":
		return "ecdsa-sha2-nistp521"
	default:
		return ""
	}
}

// parseKeyBlob decodes an SSH public key blob from r, consuming only the
// key's own fields (the blob is nested without an enclosing length prefix
// inside certificates). The canonical wire representation is retained so
// signatures and fingerprints stay byte-stable.
func parseKeyBlob(r *wireReader) (PublicKey, error) {
	start := r.pos
	keyType := r.string()
	if r.err != nil {
		return nil, ErrMalformedPublicKey
	}

	k := &publicKey{keyType: keyType}
	switch keyType {
	case "ssh-ed25519":
		raw := r.stringBytes()
		if r.err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, ErrMalformedPublicKey
		}
		k.ed25519 = ed25519.PublicKey(append([]byte(nil), raw...))

	case "ssh-rsa":
		eBytes := r.stringBytes()
		nBytes := r.stringBytes()
		if r.err != nil {
			return nil, ErrMalformedPublicKey
		}
		e := new(big.Int).SetBytes(eBytes)
		n := new(big.Int).SetBytes(nBytes)
		if e.Sign() <= 0 || n.Sign() <= 0 {
			return nil, ErrMalformedPublicKey
		}
		k.rsa = &rsa.PublicKey{N: n, E: int(e.Int64())}

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		curveName := r.string()
		point := r.stringBytes()
		expected := strings.TrimPrefix(keyType, "ecdsa-sha2-")
		if r.err != nil || curveName != expected {
			return nil, ErrMalformedPublicKey
		}
		curve := ecdsaCurve(curveName)
		if curve == nil {
			return nil, ErrUnsupportedKey
		}
		x, y := elliptic.Unmarshal(curve, point)
		if x == nil {
			return nil, ErrMalformedPublicKey
		}
		k.ecdsa = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}

	default:
		return nil, ErrUnsupportedKey
	}

	if r.err != nil {
		return nil, ErrMalformedPublicKey
	}
	k.wire = r.full[start:r.pos]
	return k, nil
}

func ecdsaCurve(name string) elliptic.Curve {
	switch name {
	case "nistp256":
		return elliptic.P256()
	case "nistp384":
		return elliptic.P384()
	case "nistp521":
		return elliptic.P521()
	default:
		return nil
	}
}

// marshalPublicKey produces the canonical SSH blob for keys created locally
// (test helpers); parsed keys keep the bytes they arrived with.
func marshalPublicKey(pub any) ([]byte, error) {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		if len(k) != ed25519.PublicKeySize {
			return nil, ErrMalformedPublicKey
		}
		out := writeString(nil, []byte("ssh-ed25519"))
		return writeString(out, k), nil

	case *rsa.PublicKey:
		if k.N == nil || k.E <= 0 {
			return nil, ErrMalformedPublicKey
		}
		out := writeString(nil, []byte("ssh-rsa"))
		out = writeMpint(out, big.NewInt(int64(k.E)))
		out = writeMpint(out, k.N)
		return out, nil

	case *ecdsa.PublicKey:
		name, ok := ecdsaCurveName(k.Curve)
		if !ok {
			return nil, ErrUnsupportedKey
		}
		typeName := "ecdsa-sha2-" + name
		point := elliptic.Marshal(k.Curve, k.X, k.Y)
		out := writeString(nil, []byte(typeName))
		out = writeString(out, []byte(name))
		out = writeString(out, point)
		return out, nil

	default:
		return nil, ErrUnsupportedKey
	}
}

func ecdsaCurveName(c elliptic.Curve) (string, bool) {
	switch c {
	case elliptic.P256():
		return "nistp256", true
	case elliptic.P384():
		return "nistp384", true
	case elliptic.P521():
		return "nistp521", true
	default:
		return "", false
	}
}

var (
	ErrMalformedPublicKey = errors.New("netsec: malformed ssh public key")
	ErrUnsupportedKey     = errors.New("netsec: unsupported ssh key type")
)
