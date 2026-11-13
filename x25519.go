package netsec

import (
	"crypto/ecdh"
	"math/big"
	"slices"
)

// x25519P 是 X25519 的素域阶 2^255 - 19。对端 u 值必须小于该数。
var x25519P = func() *big.Int {
	prime := new(big.Int).Lsh(big.NewInt(1), 255)
	return prime.Sub(prime, big.NewInt(19))
}()

// X25519ParsePublicKey 校验并解析对端的 X25519 公钥（RFC 7748 线格式，
// 32 字节小端 u 值）。
//
// 拒绝非 32 字节输入以及 u >= 2^255 - 19 的非规范值；此类值在约化后
// 会与小 u 冲突，是低阶/无效曲线点攻击的常见载体。
func X25519ParsePublicKey(enc []byte) (*ecdh.PublicKey, error) {
	if len(enc) != 32 {
		return nil, ErrInvalidPublicKey
	}

	u := new(big.Int).SetBytes(reverseBytes(enc)) // 线格式为小端
	if u.Cmp(x25519P) >= 0 {
		return nil, ErrInvalidPublicKey
	}

	// 32 字节且 u < p，标准库不会拒绝；保留错误处理以策安全。
	pub, err := ecdh.X25519().NewPublicKey(slices.Clone(enc))
	if err != nil {
		return nil, ErrInvalidPublicKey
	}
	return pub, nil
}

// X25519SharedSecret 先校验对端 u 值，再与本地私钥完成 X25519 ECDH。
// 协商结果为全零时返回 ErrSharedSecretZero（对端使用了低阶点，
// 共享秘密不再包含本地私钥的贡献）。
func X25519SharedSecret(priv *ecdh.PrivateKey, peerEnc []byte) ([]byte, error) {
	peer, err := X25519ParsePublicKey(peerEnc)
	if err != nil {
		return nil, err
	}
	return X25519ECDH(priv, peer)
}

// X25519ECDH 使用已校验的对端公钥完成 X25519 ECDH，并拒绝全零结果。
func X25519ECDH(priv *ecdh.PrivateKey, peer *ecdh.PublicKey) ([]byte, error) {
	if priv == nil || peer == nil {
		return nil, ErrInvalidPublicKey
	}
	if priv.Curve() != ecdh.X25519() || peer.Curve() != ecdh.X25519() {
		return nil, ErrInvalidPublicKey
	}

	secret, err := priv.ECDH(peer)
	if err != nil {
		return nil, err
	}
	if isAllZero(secret) {
		return nil, ErrSharedSecretZero
	}
	return secret, nil
}

func reverseBytes(in []byte) []byte {
	out := make([]byte, len(in))
	for i, b := range in {
		out[len(in)-1-i] = b
	}
	return out
}

func isAllZero(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
