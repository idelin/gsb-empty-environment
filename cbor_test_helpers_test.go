package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"math/big"
	"time"
)

// --- small self-contained CBOR decoder used by the test callbacks only ---

type cborDecoder struct {
	data []byte
}

func cborDecode(data []byte) (any, error) {
	d := &cborDecoder{data: data}
	v, err := d.decode()
	if err != nil {
		return nil, err
	}
	if len(d.data) != 0 {
		return nil, errors.New("cbor: trailing bytes")
	}
	return v, nil
}

func (d *cborDecoder) decode() (any, error) {
	if len(d.data) == 0 {
		return nil, errors.New("cbor: unexpected end")
	}
	ib := d.data[0]
	d.data = d.data[1:]
	major := ib >> 5
	switch major {
	case 0:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		return arg, nil
	case 1:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		return -int64(arg) - 1, nil
	case 2:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		return d.take(int(arg)), nil
	case 3:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		return string(d.take(int(arg))), nil
	case 4:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		arr := make([]any, arg)
		for i := range arr {
			if arr[i], err = d.decode(); err != nil {
				return nil, err
			}
		}
		return arr, nil
	case 5:
		arg, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		m := make(map[any]any, arg)
		for i := uint64(0); i < arg; i++ {
			k, err := d.decode()
			if err != nil {
				return nil, err
			}
			v, err := d.decode()
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case 6:
		tag, err := d.arg(ib & 0x1f)
		if err != nil {
			return nil, err
		}
		if tag == 24 {
			// Encoded CBOR data item: return the inner byte string to the
			// test assertions without decoding it.
			head := d.data[0]
			d.data = d.data[1:]
			arg, err := d.arg(head & 0x1f)
			if err != nil {
				return nil, err
			}
			return d.take(int(arg)), nil
		}
		return d.decode()
	case 7:
		switch ib & 0x1f {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22, 23:
			return nil, nil
		case 25:
			bits := d.take(2)
			if len(bits) != 2 {
				return nil, errors.New("cbor: truncated float16")
			}
			return math.Float64frombits(float16ToBits(binary.BigEndian.Uint16(bits))), nil
		case 26:
			bits := d.take(4)
			if len(bits) != 4 {
				return nil, errors.New("cbor: truncated float32")
			}
			return float64(math.Float32frombits(binary.BigEndian.Uint32(bits))), nil
		case 27:
			bits := d.take(8)
			if len(bits) != 8 {
				return nil, errors.New("cbor: truncated float64")
			}
			return math.Float64frombits(binary.BigEndian.Uint64(bits)), nil
		default:
			return nil, errors.New("cbor: unsupported simple value")
		}
	default:
		return nil, errors.New("cbor: unsupported major type")
	}
}

func (d *cborDecoder) arg(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		return uint64(d.take(1)[0]), nil
	case ai == 25:
		return uint64(binary.BigEndian.Uint16(d.take(2))), nil
	case ai == 26:
		return uint64(binary.BigEndian.Uint32(d.take(4))), nil
	case ai == 27:
		return binary.BigEndian.Uint64(d.take(8)), nil
	default:
		return 0, errors.New("cbor: unsupported additional info")
	}
}

func (d *cborDecoder) take(n int) []byte {
	if n > len(d.data) {
		return nil
	}
	out := d.data[:n]
	d.data = d.data[n:]
	return out
}

// --- ES256 COSE_Sign1 helpers ---

func testNewHash(algorithm string) (HasherSize, error) {
	switch algorithm {
	case "SHA-256", "":
		return sha256.New(), nil
	case "SHA-384":
		return crypto.SHA384.New(), nil
	case "SHA-512":
		return crypto.SHA512.New(), nil
	default:
		return nil, errors.New("unsupported digest algorithm " + algorithm)
	}
}

// coseEmptyProtected is the empty protected header map encoded as a
// zero-length byte string.
var coseEmptyProtected = []byte{}

// signCOSESign1 builds an untagged COSE_Sign1 structure authenticating
// payload with an ES256 signature (raw R||S, fixed 64 bytes).
func signCOSESign1(key *ecdsa.PrivateKey, payload []byte) ([]byte, error) {
	sigStructure := []any{
		"Signature1",
		coseEmptyProtected,
		[]byte{}, // external AAD (empty bstr)
		payload,
	}
	toSign, err := mustEncode(sigStructure)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(toSign)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	coseSign1 := []any{
		[]byte{},      // protected
		map[any]any{}, // unprotected
		payload,
		sig,
	}
	return mustEncode(coseSign1)
}

// verifyCOSESign1 is the DeviceSignatureVerifier callback used in tests.
func verifyCOSESign1(deviceKey any, deviceAuthCOSE, deviceAuthentication []byte) error {
	key, ok := deviceKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("device key is not an ECDSA public key")
	}
	decoded, err := cborDecode(deviceAuthCOSE)
	if err != nil {
		return err
	}
	sign1, ok := decoded.([]any)
	if !ok || len(sign1) != 4 {
		return errors.New("malformed COSE_Sign1")
	}
	protected, ok := sign1[0].([]byte)
	if !ok {
		return errors.New("malformed protected header")
	}
	sig, ok := sign1[3].([]byte)
	if !ok || len(sig) != 64 {
		return errors.New("malformed signature")
	}
	sigStructure := []any{
		"Signature1",
		protected,
		[]byte{},
		deviceAuthentication,
	}
	toSign, err := mustEncode(sigStructure)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(toSign)
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(key, digest[:], r, s) {
		return errors.New("ecdsa verification failed")
	}
	return nil
}

// mustEncode canonical-encodes through the package encoder for test fixtures.
func mustEncode(value any) ([]byte, error) {
	buf := &bytes.Buffer{}
	if err := cborEncode(buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodedSessionTranscript wraps the transcript content in tag 24 as it
// appears on the wire.
func encodedSessionTranscript(content []byte) []byte {
	buf := &bytes.Buffer{}
	encodeHead(buf, cborTag, cborTagEncodedCBOR)
	encodeHead(buf, cborBytes, uint64(len(content)))
	buf.Write(content)
	return buf.Bytes()
}

var _ = time.Now
