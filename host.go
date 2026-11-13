package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"
)

// Host 是文件系统宿主，持有主句柄并派生各目录的名密钥。
type Host struct {
	master []byte
	macFn  func(key, msg []byte) []byte
	rand   io.Reader
	serial atomic.Uint64
}

// HostOption 用于测试或集成时替换宿主原语。
type HostOption func(*Host)

// WithMACFunc 替换默认的 HMAC-SHA256（例如注入会碰撞的 HMAC 替身）。
func WithMACFunc(fn func(key, msg []byte) []byte) HostOption {
	return func(h *Host) { h.macFn = fn }
}

// WithRandomSource 替换随机源。
func WithRandomSource(r io.Reader) HostOption {
	return func(h *Host) { h.rand = r }
}

// NewHost 创建宿主；masterHandle 为派生名密钥的主句柄。
func NewHost(masterHandle []byte, opts ...HostOption) (*Host, error) {
	if len(masterHandle) == 0 {
		return nil, errors.New("netsec: empty master handle")
	}
	h := &Host{
		master: append([]byte(nil), masterHandle...),
		macFn:  hmacSHA256,
		rand:   rand.Reader,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// NewDirectory 冻结策略并创建空目录。
func (h *Host) NewDirectory(p Policy) (*Directory, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	d := &Directory{
		host:    h,
		policy:  p,
		current: p.KeyGeneration,
		gens:    map[uint64]struct{}{p.KeyGeneration: {}},
		buckets: make(map[[20]byte][]*entry),
		serial:  h.serial.Add(1),
	}
	return d, nil
}

// nameKey 派生名密钥：HMAC(masterHandle, "name"||directoryID||keyGeneration||directoryNonce)。
func (h *Host) nameKey(directoryID [16]byte, gen uint64, nonce [32]byte) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], gen)
	msg := make([]byte, 0, 4+16+8+32)
	msg = append(msg, "name"...)
	msg = append(msg, directoryID[:]...)
	msg = append(msg, b[:]...)
	msg = append(msg, nonce[:]...)
	return h.macFn(h.master, msg)
}

// lookupKey 计算磁盘查找键：HMAC(nameKey, "lookup"||规范名) 的前 20 字节。
func (h *Host) lookupKey(nameKey []byte, canonical string) [20]byte {
	msg := make([]byte, 0, 6+len(canonical))
	msg = append(msg, "lookup"...)
	msg = append(msg, canonical...)
	mac := h.macFn(nameKey, msg)
	var out [20]byte
	copy(out[:], mac)
	return out
}
