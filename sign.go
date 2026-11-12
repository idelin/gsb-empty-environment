package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Target 是消息的授权目标：接收方与操作。
type Target struct {
	Recipient string
	Operation string
}

// macOver 对覆盖路径逐条做长度前缀拼接后计算 HMAC。
func macOver(key []byte, covers []string, root *Node) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("netsec/sig/v1"))
	var lb [4]byte
	for _, p := range covers {
		sub, err := root.Get(p)
		if err != nil {
			return nil, fmt.Errorf("netsec: 覆盖路径不可解析: %w", err)
		}
		enc := Canonical(sub)
		binary.BigEndian.PutUint32(lb[:], uint32(len(p)))
		h.Write(lb[:])
		h.Write([]byte(p))
		binary.BigEndian.PutUint32(lb[:], uint32(len(enc)))
		h.Write(lb[:])
		h.Write(enc)
	}
	return h.Sum(nil), nil
}

// Sign 在 sigPath 处挂上签名节点，覆盖声明为 covers。
func Sign(root *Node, key []byte, sigPath string, covers []string) error {
	mac, err := macOver(key, covers, root)
	if err != nil {
		return err
	}
	sig := &Node{
		Kind:   KindSig,
		Covers: append([]string(nil), covers...),
		Bytes:  mac,
	}
	return root.Set(sigPath, sig)
}

// Verify 是原验证谓词：按签名节点声明的覆盖路径重算并比对 MAC。
func Verify(root *Node, key []byte, sigPath string) bool {
	sn, err := root.Get(sigPath)
	if err != nil || sn.Kind != KindSig {
		return false
	}
	mac, err := macOver(key, sn.Covers, root)
	if err != nil {
		return false
	}
	return hmac.Equal(mac, sn.Bytes)
}

// CoveredPaths 读取签名节点声明的覆盖路径。
func CoveredPaths(root *Node, sigPath string) ([]string, error) {
	sn, err := root.Get(sigPath)
	if err != nil {
		return nil, err
	}
	if sn.Kind != KindSig {
		return nil, errors.New("netsec: 签名路径处不是签名节点")
	}
	return append([]string(nil), sn.Covers...), nil
}

// AuthTarget 从消息中提取授权目标（接收方与操作）。
func AuthTarget(root *Node, toPath, opPath string) (Target, error) {
	var t Target
	to, err := root.Get(toPath)
	if err != nil {
		return t, err
	}
	if to.Kind != KindString {
		return t, fmt.Errorf("netsec: %q 不是字符串字段", toPath)
	}
	op, err := root.Get(opPath)
	if err != nil {
		return t, err
	}
	if op.Kind != KindString {
		return t, fmt.Errorf("netsec: %q 不是字符串字段", opPath)
	}
	t.Recipient = to.Str
	t.Operation = op.Str
	return t, nil
}
