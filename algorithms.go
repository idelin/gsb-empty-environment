package netsec

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"sync"
)

// RFC 8879 第 3 节定义的 CertificateCompressionAlgorithm 取值。
const (
	AlgBrotli uint16 = 1
	AlgZstd   uint16 = 2
	AlgZlib   uint16 = 3
)

// Compressor 实现一种证书压缩算法。dict 为本题约定的预共享字典,
// 为 nil 表示不使用字典。
type Compressor interface {
	// Compress 压缩证书链字节。
	Compress(chain, dict []byte) ([]byte, error)
	// Decompress 解压, 输出不得超过 maxLen 字节(用于抵御解压炸弹)。
	Decompress(data, dict []byte, maxLen int) ([]byte, error)
}

// Registry 是服务端支持的压缩算法注册表, 并发安全。
type Registry struct {
	mu   sync.RWMutex
	impl map[uint16]Compressor
}

func NewRegistry() *Registry {
	return &Registry{impl: make(map[uint16]Compressor)}
}

func (r *Registry) Register(id uint16, c Compressor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.impl[id] = c
}

func (r *Registry) Get(id uint16) (Compressor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.impl[id]
	return c, ok
}

// ZlibCompressor 基于标准库 compress/zlib, 支持预设字典。
type ZlibCompressor struct{}

func (ZlibCompressor) Compress(chain, dict []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevelDict(&buf, zlib.BestCompression, dict)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(chain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (ZlibCompressor) Decompress(data, dict []byte, maxLen int) ([]byte, error) {
	r, err := zlib.NewReaderDict(bytes.NewReader(data), dict)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// 多读 1 字节, 以便调用方能区分"恰好 maxLen"与"超出 maxLen"。
	out, err := io.ReadAll(io.LimitReader(r, int64(maxLen)+1))
	if err != nil {
		return nil, fmt.Errorf("zlib: decompress: %w", err)
	}
	return out, nil
}
