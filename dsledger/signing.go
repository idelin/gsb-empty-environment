package dsledger

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
)

// 版本化的签名域分隔符，防止与其他协议的消息混淆。
var signingDomain = []byte("dsledger-share/v1\n")

func putUint64(dst []byte, v uint64) {
	binary.BigEndian.PutUint64(dst, v)
}

// SigningBytes 返回份额消息被签名/验签的规范字节串。
//
// 布局（全部定长，无歧义、无未初始化填充参与）：
//
//	域分隔符 signingDomain
//	链 ID 长度(uint64 BE) + 链 ID 原始字节
//	高度(uint64 BE)
//	轮次(uint64 BE)
//	阶段(uint8)
//	区块摘要(32B)
//	父摘要(32B)
//
// Signer 与 Sig 字段不参与，签名身份由验签时使用的成员公钥确定。
func SigningBytes(chainID string, height, round uint64, phase Phase, block, parent Digest) []byte {
	out := make([]byte, 0, len(signingDomain)+8+len(chainID)+8+8+1+32+32)
	out = append(out, signingDomain...)
	size := make([]byte, 8)
	putUint64(size, uint64(len(chainID)))
	out = append(out, size...)
	out = append(out, chainID...)
	putUint64(size, height)
	out = append(out, size...)
	putUint64(size, round)
	out = append(out, size...)
	out = append(out, byte(phase))
	out = append(out, block[:]...)
	out = append(out, parent[:]...)
	return out
}

// SignShare 用成员私钥对份额内容签名并返回完整份额。
// 私钥与成员公钥必须匹配，否则返回错误。
func SignShare(priv ed25519.PrivateKey, chainID string, signer MemberID,
	height, round uint64, phase Phase, block, parent Digest) (Share, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Share{}, fmt.Errorf("dsledger: invalid ed25519 private key size")
	}
	if !phase.Valid() {
		return Share{}, fmt.Errorf("dsledger: invalid phase %d", uint8(phase))
	}
	msg := SigningBytes(chainID, height, round, phase, block, parent)
	sig := ed25519.Sign(priv, msg)
	return Share{
		ChainID: chainID,
		Signer:  signer,
		Height:  height,
		Round:   round,
		Phase:   phase,
		Block:   block,
		Parent:  parent,
		Sig:     sig,
	}, nil
}

// VerifyShare 仅依赖固定成员清单独立验证份额：
// 链 ID 匹配、阶段合法、签名者在清单内且签名对规范载荷有效。
func VerifyShare(roster *Roster, s Share) error {
	if roster == nil {
		return fmt.Errorf("dsledger: nil roster")
	}
	if s.ChainID != roster.ChainID() {
		return fmt.Errorf("dsledger: chain id mismatch: got %q want %q", s.ChainID, roster.ChainID())
	}
	if !s.Phase.Valid() {
		return fmt.Errorf("dsledger: invalid phase %d", uint8(s.Phase))
	}
	member, ok := roster.Member(s.Signer)
	if !ok {
		return fmt.Errorf("dsledger: unknown signer %q", s.Signer)
	}
	if len(s.Sig) != ed25519.SignatureSize {
		return fmt.Errorf("dsledger: invalid signature size %d", len(s.Sig))
	}
	msg := SigningBytes(s.ChainID, s.Height, s.Round, s.Phase, s.Block, s.Parent)
	if !ed25519.Verify(member.PublicKey, msg, s.Sig) {
		return fmt.Errorf("dsledger: invalid share signature from %q", s.Signer)
	}
	return nil
}
