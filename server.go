package netsec

import (
	"slices"
	"sync"
)

// Server 在 TLS 服务端握手路径上提供证书压缩 (RFC 8879)。
type Server struct {
	reg        *Registry
	dicts      *DictStore
	cache      *compressCache
	preference []uint16 // 服务端算法偏好, 按优先级排序
}

func NewServer(preference []uint16) *Server {
	s := &Server{
		reg:        NewRegistry(),
		dicts:      NewDictStore(),
		cache:      newCompressCache(),
		preference: slices.Clone(preference),
	}
	s.reg.Register(AlgZlib, ZlibCompressor{})
	return s
}

// Registry 暴露算法注册表, 便于接入 brotli/zstd 等实现。
func (s *Server) Registry() *Registry { return s.reg }

// Dicts 暴露字典仓库, 供租户控制面注册字典。
func (s *Server) Dicts() *DictStore { return s.dicts }

// ConnState 记录单条连接的压缩证书收发状态。
// 同一连接只允许发送(以及接收)一次压缩证书。
type ConnState struct {
	mu                 sync.Mutex
	compressedSent     bool
	compressedReceived bool
}

func (c *ConnState) markSent() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.compressedSent {
		return alert(AlertUnexpectedMessage, "compressed certificate already sent on this connection")
	}
	c.compressedSent = true
	return nil
}

func (c *ConnState) markReceived() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.compressedReceived {
		return alert(AlertUnexpectedMessage, "compressed certificate already received on this connection")
	}
	c.compressedReceived = true
	return nil
}

// CertMessage 是握手路径的输出: 要么携带压缩证书, 要么回退为未压缩证书。
type CertMessage struct {
	Compressed         bool
	Algorithm          uint16
	DictID             uint32
	UncompressedLength uint32
	Body               []byte // 压缩后的字节, 或未压缩时的原始证书链
}

// CompressCertificateFor 从客户端 compress_certificate 扩展列出的算法中
// 按服务端偏好选择并压缩; 无共同算法或压缩后不变小时回退为未压缩证书。
// dictID 非 0 时使用本题约定的字典扩展, 字典必须属于 sni 标识的租户。
func (s *Server) CompressCertificateFor(conn *ConnState, advertised []uint16, chain []byte, sni string, dictID uint32) (*CertMessage, error) {
	alg, ok := s.pickAlgorithm(advertised)
	if !ok {
		return &CertMessage{Body: chain}, nil
	}
	return s.compressWith(conn, alg, chain, sni, dictID)
}

// CompressCertificateWith 使用指定算法压缩; 该算法必须出现在客户端
// compress_certificate 扩展中, 否则返回 illegal_parameter。
func (s *Server) CompressCertificateWith(conn *ConnState, alg uint16, advertised []uint16, chain []byte, sni string, dictID uint32) (*CertMessage, error) {
	if !slices.Contains(advertised, alg) {
		return nil, alert(AlertIllegalParameter,
			"algorithm %d not in client's compress_certificate extension", alg)
	}
	return s.compressWith(conn, alg, chain, sni, dictID)
}

func (s *Server) pickAlgorithm(advertised []uint16) (uint16, bool) {
	for _, alg := range s.preference {
		if _, ok := s.reg.Get(alg); !ok {
			continue
		}
		if slices.Contains(advertised, alg) {
			return alg, true
		}
	}
	return 0, false
}

func (s *Server) compressWith(conn *ConnState, alg uint16, chain []byte, sni string, dictID uint32) (*CertMessage, error) {
	comp, ok := s.reg.Get(alg)
	if !ok {
		return nil, alert(AlertInternalError, "algorithm %d not registered", alg)
	}
	var dict []byte
	if dictID != 0 {
		d, err := s.dicts.forTenant(dictID, sni)
		if err != nil {
			return nil, err
		}
		dict = d.Data
	}
	key := keyFor(alg, dictID, chain)
	body, ok := s.cache.get(key)
	if !ok {
		var err error
		body, err = comp.Compress(chain, dict)
		if err != nil {
			return nil, alert(AlertInternalError, "compress: %v", err)
		}
		s.cache.put(key, body)
	}
	// 压缩后不比原文小, 改发未压缩证书。
	if len(body) >= len(chain) {
		return &CertMessage{Body: chain}, nil
	}
	if err := conn.markSent(); err != nil {
		return nil, err
	}
	return &CertMessage{
		Compressed:         true,
		Algorithm:          alg,
		DictID:             dictID,
		UncompressedLength: uint32(len(chain)),
		Body:               body,
	}, nil
}

// DecompressCertificate 处理对端发来的 CompressedCertificate。
// uncompressed_length 不得大于 2^24-1, 且解压结果长度必须恰等于该值,
// 否则按 bad_certificate 中止握手。sni 为当前连接所属租户。
func (s *Server) DecompressCertificate(conn *ConnState, msg []byte, sni string) ([]byte, error) {
	alg, uncompressedLen, dictID, body, err := ParseCompressedCertificate(msg)
	if err != nil {
		return nil, err
	}
	return s.decompress(conn, alg, dictID, uncompressedLen, body, sni)
}

func (s *Server) decompress(conn *ConnState, alg uint16, dictID uint32, uncompressedLen uint32, body []byte, sni string) ([]byte, error) {
	if uncompressedLen > MaxUncompressedLength {
		return nil, alert(AlertBadCertificate,
			"uncompressed_length %d exceeds 2^24-1", uncompressedLen)
	}
	comp, ok := s.reg.Get(alg)
	if !ok {
		return nil, alert(AlertIllegalParameter, "unsupported compression algorithm %d", alg)
	}
	var dict []byte
	if dictID != 0 {
		d, err := s.dicts.forTenant(dictID, sni)
		if err != nil {
			return nil, err
		}
		dict = d.Data
	}
	out, err := comp.Decompress(body, dict, int(uncompressedLen))
	if err != nil {
		return nil, alert(AlertBadCertificate, "decompress: %v", err)
	}
	if len(out) != int(uncompressedLen) {
		return nil, alert(AlertBadCertificate,
			"decompressed length %d does not match declared %d", len(out), uncompressedLen)
	}
	if err := conn.markReceived(); err != nil {
		return nil, err
	}
	return out, nil
}

// cacheStats 供测试与可观测性使用。
func (s *Server) cacheStats() (entries, hits, misses uint64) {
	return s.cache.stats()
}
