package netsec

import (
	"bytes"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// 固定域分离标签。挑战与批量系数使用互不相同的标签，避免跨场景哈希复用。
const (
	labelChallenge = "P256-SCHNORR-V1/CHALLENGE"
	labelCoeff     = "P256-SCHNORR-V1/BATCH-COEFF"

	// MaxBatchSize 是单个批次允许的最大条目数。
	MaxBatchSize = 256

	scalarSize = 32
	pointSize  = 65
)

var (
	curve = elliptic.P256()
	q     = curve.Params().N // P-256 群阶

	errEmptyBatch    = errors.New("netsec: batch must contain between 1 and 256 items")
	errSeedLength    = errors.New("netsec: seed must be exactly 32 bytes")
	errPointEncoding = errors.New("netsec: point must be a 65-byte uncompressed SEC1 encoding (0x04 prefix)")
	errPointInvalid  = errors.New("netsec: point is not a valid non-infinity P-256 point")
	errScalarRange   = errors.New("netsec: scalar must be 32 bytes and in the range [1, q-1]")
)

// ItemError 指出第 Index 个批次条目格式不合法，验证不会继续进行。
type ItemError struct {
	Index  int
	Reason error
}

func (e *ItemError) Error() string {
	return fmt.Sprintf("netsec: item %d malformed: %v", e.Index, e.Reason)
}

func (e *ItemError) Unwrap() error { return e.Reason }

// decodePoint 强制规范点编码：65 字节、0x04 前缀、落在曲线上、非无穷远点。
func decodePoint(enc []byte) (*big.Int, *big.Int, error) {
	if len(enc) != pointSize || enc[0] != 0x04 {
		return nil, nil, errPointEncoding
	}
	x, y := elliptic.Unmarshal(curve, enc)
	if x == nil {
		return nil, nil, errPointInvalid
	}
	return x, y, nil
}

// decodeScalar 强制规范标量：32 字节大端、约化到 [1, q-1]。
func decodeScalar(enc []byte) (*big.Int, error) {
	if len(enc) != scalarSize {
		return nil, errScalarRange
	}
	v := new(big.Int).SetBytes(enc)
	if v.Sign() == 0 || v.Cmp(q) >= 0 {
		return nil, errScalarRange
	}
	return v, nil
}

func encodeScalar(v *big.Int) []byte {
	enc := make([]byte, scalarSize)
	v.FillBytes(enc)
	return enc
}

// hashModQ 计算 sha256(parts...) mod q，并返回该值是否为零。
func hashModQ(parts ...[]byte) (*big.Int, bool) {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	v := new(big.Int).Mod(new(big.Int).SetBytes(h.Sum(nil)), q)
	return v, v.Sign() == 0
}

func writeLenBytes(b *bytes.Buffer, raw []byte) {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(raw)))
	b.Write(l[:])
	b.Write(raw)
}

// challenge 计算 e = SHA256(固定标签 || 公钥 || R || 消息长度 || 消息) mod q。
// pub 与 r 均为规范点编码，消息长度以 8 字节大端前缀编码。
func challenge(pub, r, msg []byte) *big.Int {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(msg)))
	e, _ := hashModQ([]byte(labelChallenge), pub, r, l[:], msg)
	return e
}

// GenerateKey 生成 P-256 Schnorr 密钥对，返回 32 字节大端私钥与规范公钥。
func GenerateKey(rng io.Reader) (priv, pub []byte, err error) {
	if rng == nil {
		rng = rand.Reader
	}
	d, x, y, err := elliptic.GenerateKey(curve, rng)
	if err != nil {
		return nil, nil, err
	}
	priv = encodeScalar(new(big.Int).SetBytes(d))
	pub = elliptic.Marshal(curve, x, y)
	return priv, pub, nil
}

// Sign 生成 Schnorr 签名 (R, s)。R 为规范点编码，s 为 32 字节规范标量。
// 随机数 k 来自 rng（默认 crypto/rand），s == 0 时重新取样。
func Sign(priv, msg []byte, rng io.Reader) (rBytes, sBytes []byte, err error) {
	d, err := decodeScalar(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("netsec: invalid private key: %w", err)
	}
	if rng == nil {
		rng = rand.Reader
	}
	pubX, pubY := curve.ScalarBaseMult(priv)
	pub := elliptic.Marshal(curve, pubX, pubY)

	for attempt := 0; attempt < 16; attempt++ {
		k, x, y, err := elliptic.GenerateKey(curve, rng)
		if err != nil {
			return nil, nil, err
		}
		kInt := new(big.Int).SetBytes(k)
		if kInt.Sign() == 0 {
			continue
		}
		rBytes = elliptic.Marshal(curve, x, y)
		e := challenge(pub, rBytes, msg)
		s := new(big.Int).Mul(e, d)
		s.Add(s, kInt)
		s.Mod(s, q)
		if s.Sign() == 0 {
			continue
		}
		return rBytes, encodeScalar(s), nil
	}
	return nil, nil, errors.New("netsec: failed to generate a non-zero s after retries")
}

// VerifySingle 验证单份签名：sG = R + eP。
// 返回 (false, nil) 表示密码学验证失败；返回 error 表示条目格式不合法。
func VerifySingle(pub, r, s, msg []byte) (bool, error) {
	px, py, err := decodePoint(pub)
	if err != nil {
		return false, err
	}
	rx, ry, err := decodePoint(r)
	if err != nil {
		return false, err
	}
	sInt, err := decodeScalar(s)
	if err != nil {
		return false, err
	}

	e := challenge(pub, r, msg)

	lhsX, lhsY := scalarBaseMult(encodeScalar(sInt))
	epX, epY := scalarMult(px, py, encodeScalar(e)) // e 可能为 0，此时得到无穷远点
	rhsX, rhsY := addPoint(rx, ry, epX, epY)
	return pointsEqual(lhsX, lhsY, rhsX, rhsY), nil
}

func addPoint(ax, ay, bx, by *big.Int) (*big.Int, *big.Int) {
	if ax == nil { // a 为无穷远点
		return bx, by
	}
	if bx == nil { // b 为无穷远点
		return ax, ay
	}
	return curve.Add(ax, ay, bx, by)
}

func pointsEqual(ax, ay, bx, by *big.Int) bool {
	if isInfinity(ax, ay) || isInfinity(bx, by) {
		return isInfinity(ax, ay) && isInfinity(bx, by)
	}
	return ax.Cmp(bx) == 0 && ay.Cmp(by) == 0
}

// isInfinity 判定点是否为无穷远点。Go 标准库 P-256 内部以 (0,0) 表示，
// 解码得到的有限点不可能是 (0,0)，因此同时兼容 nil 与 (0,0) 两种形态。
func isInfinity(x, y *big.Int) bool {
	return x == nil || y == nil || (x.Sign() == 0 && y.Sign() == 0)
}

// scalarBaseMult 计算标量乘基点；标量为 0 时返回无穷远点坐标 (nil, nil)。
func scalarBaseMult(scalar []byte) (*big.Int, *big.Int) {
	x, y := curve.ScalarBaseMult(scalar)
	if isInfinity(x, y) {
		return nil, nil
	}
	return x, y
}

// scalarMult 计算点标量乘；标量为 0 时返回无穷远点坐标 (nil, nil)。
func scalarMult(px, py *big.Int, scalar []byte) (*big.Int, *big.Int) {
	x, y := curve.ScalarMult(px, py, scalar)
	if isInfinity(x, y) {
		return nil, nil
	}
	return x, y
}
