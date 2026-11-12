package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
)

var (
	ErrBadSignature = errors.New("netsec: signature verification failed")
	ErrBadKey       = errors.New("netsec: bad public key in current image")
	ErrBadSigFormat = errors.New("netsec: bad signature section content")
)

// Digest 计算文件摘要：将 .sha256_sig 与 .sig_key 两节内容置零后的 SHA-256。
func Digest(data []byte, secs *Sections) [sha256.Size]byte {
	buf := make([]byte, len(data))
	copy(buf, data)
	for _, sec := range []Section{secs.Sig, secs.Key} {
		clear(buf[sec.Offset : sec.Offset+sec.Size])
	}
	return sha256.Sum256(buf)
}

// VerifyUpdate 判定候选更新文件是否可信：
//  1. 从当前运行镜像提取可信公钥（新文件自带的 .sig_key 一律忽略）；
//  2. 校验候选文件节边界与重叠；
//  3. 用可信公钥验证候选文件 .sha256_sig 对摘要的签名；
//  4. 解析并返回候选文件的更新源信息。
func VerifyUpdate(currentImage, candidate []byte) (*UpdateInfo, error) {
	curSecs, err := FindSections(currentImage)
	if err != nil {
		return nil, fmt.Errorf("current image: %w", err)
	}
	trustedKey := trimPadding(curSecs.Key.Bytes(currentImage))
	if len(trustedKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: len=%d", ErrBadKey, len(trustedKey))
	}

	newSecs, err := FindSections(candidate)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	sig := trimPadding(newSecs.Sig.Bytes(candidate))
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: len=%d", ErrBadSigFormat, len(sig))
	}
	digest := Digest(candidate, newSecs)
	if !ed25519.Verify(ed25519.PublicKey(trustedKey), digest[:], sig) {
		return nil, ErrBadSignature
	}

	info, err := ParseUpdateInfo(string(newSecs.UpdateInfo.Bytes(candidate)))
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	return info, nil
}

// trimPadding 去掉节内容尾部的 NUL 填充。
func trimPadding(b []byte) []byte {
	return bytes.TrimRight(b, "\x00")
}
