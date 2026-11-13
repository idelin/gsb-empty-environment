package netsec

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"errors"
	"math/big"
)

// 对端公钥校验失败时返回的哨兵错误，调用方可用 errors.Is 判定。
var (
	// ErrInvalidPublicKey 表示对端公钥编码非法、坐标非规范或点不在曲线上。
	ErrInvalidPublicKey = errors.New("netsec: invalid peer public key")
	// ErrSharedSecretZero 表示密钥协商结果为全零（对端提供了低阶/贡献性失效的点）。
	ErrSharedSecretZero = errors.New("netsec: derived shared secret is all zero")
)

// P-256 曲线参数：y^2 = x^3 + a*x + b (mod p)，其中 a = p - 3。
var (
	p256P *big.Int
	p256A *big.Int
	p256B *big.Int
)

func init() {
	params := elliptic.P256().Params()
	p256P = new(big.Int).Set(params.P)
	p256B = new(big.Int).Set(params.B)
	p256A = new(big.Int).Sub(p256P, big.NewInt(3))
}

// P256ParsePublicKey 校验并解析对端的 P-256 公钥。
//
// 接受 SEC1 两种仿射编码：
//   - 未压缩：0x04 || X || Y，共 65 字节；
//   - 压缩：0x02/0x03 || X，共 33 字节，需在 F_p 上解平方根恢复 Y。
//
// 拒绝无穷远点、混合编码（0x06/0x07）、长度或前缀错误、坐标大于等于 p
// 的非规范值，以及不满足曲线方程的点。返回的 *ecdh.PublicKey 已经过
// crypto/ecdh 的二次校验，可直接用于 ECDH。
func P256ParsePublicKey(enc []byte) (*ecdh.PublicKey, error) {
	x, y, err := p256DecodePoint(enc)
	if err != nil {
		return nil, err
	}

	// 重新编码为未压缩形式交给标准库，做纵深防御校验。
	uncompressed := make([]byte, 1+2*32)
	uncompressed[0] = 0x04
	x.FillBytes(uncompressed[1:33])
	y.FillBytes(uncompressed[33:65])

	pub, err := ecdh.P256().NewPublicKey(uncompressed)
	if err != nil {
		return nil, ErrInvalidPublicKey
	}
	return pub, nil
}

// P256SharedSecret 先校验对端公钥编码，再与本地私钥完成 P-256 ECDH。
func P256SharedSecret(priv *ecdh.PrivateKey, peerEnc []byte) ([]byte, error) {
	peer, err := P256ParsePublicKey(peerEnc)
	if err != nil {
		return nil, err
	}
	return P256ECDH(priv, peer)
}

// P256ECDH 使用已校验的对端公钥完成 P-256 ECDH。
func P256ECDH(priv *ecdh.PrivateKey, peer *ecdh.PublicKey) ([]byte, error) {
	if priv == nil || peer == nil {
		return nil, ErrInvalidPublicKey
	}
	if priv.Curve() != ecdh.P256() || peer.Curve() != ecdh.P256() {
		return nil, ErrInvalidPublicKey
	}
	return priv.ECDH(peer)
}

// p256DecodePoint 解码 SEC1 点并执行完整的公钥校验。
func p256DecodePoint(enc []byte) (x, y *big.Int, err error) {
	// 单字节 0x00 是 SEC1 对无穷远点的编码，显式拒绝。
	if len(enc) == 1 && enc[0] == 0x00 {
		return nil, nil, ErrInvalidPublicKey
	}

	switch {
	case len(enc) == 65 && enc[0] == 0x04: // 未压缩点
		x = new(big.Int).SetBytes(enc[1:33])
		y = new(big.Int).SetBytes(enc[33:65])
	case len(enc) == 33 && (enc[0] == 0x02 || enc[0] == 0x03): // 压缩点
		x = new(big.Int).SetBytes(enc[1:33])
		if x.Cmp(p256P) >= 0 {
			return nil, nil, ErrInvalidPublicKey
		}
		y, err = p256DecompressY(x, enc[0] == 0x03)
		if err != nil {
			return nil, nil, err
		}
	default:
		// 其它长度、0x00/0x05/0x06/0x07 等前缀（含混合编码）一律拒绝。
		return nil, nil, ErrInvalidPublicKey
	}

	// 坐标必须落在 [0, p) 内，拒绝非规范编码。
	if x.Sign() < 0 || y.Sign() < 0 || x.Cmp(p256P) >= 0 || y.Cmp(p256P) >= 0 {
		return nil, nil, ErrInvalidPublicKey
	}
	if !p256IsOnCurve(x, y) {
		return nil, nil, ErrInvalidPublicKey
	}
	return x, y, nil
}

// p256DecompressY 由压缩点的 x 与奇偶标志恢复 y。
// P-256 的素域满足 p ≡ 3 (mod 4)，故 y = rhs^((p+1)/4) mod p。
func p256DecompressY(x *big.Int, yOdd bool) (*big.Int, error) {
	rhs := p256CurveRHS(x)

	exp := new(big.Int).Add(p256P, big.NewInt(1))
	exp.Rsh(exp, 2) // (p+1)/4
	y := new(big.Int).Exp(rhs, exp, p256P)

	// 校验 y^2 == rhs：不成立说明 rhs 是二次非剩余，点不在曲线上。
	ySquared := new(big.Int).Mul(y, y)
	ySquared.Mod(ySquared, p256P)
	if ySquared.Cmp(rhs) != 0 {
		return nil, ErrInvalidPublicKey
	}

	if y.Bit(0) != boolToBit(yOdd) {
		y.Sub(p256P, y) // 取另一个平方根 -y
	}
	return y, nil
}

// p256IsOnCurve 判断仿射点是否满足 y^2 = x^3 + a*x + b (mod p)。
func p256IsOnCurve(x, y *big.Int) bool {
	lhs := new(big.Int).Exp(y, big.NewInt(2), p256P)
	return lhs.Cmp(p256CurveRHS(x)) == 0
}

// p256CurveRHS 计算 x^3 + a*x + b (mod p)。
func p256CurveRHS(x *big.Int) *big.Int {
	x3 := new(big.Int).Exp(x, big.NewInt(3), p256P)
	ax := new(big.Int).Mul(p256A, x)
	rhs := new(big.Int).Add(x3, ax)
	rhs.Add(rhs, p256B)
	return rhs.Mod(rhs, p256P)
}

func boolToBit(b bool) uint {
	if b {
		return 1
	}
	return 0
}
