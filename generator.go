package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// GenerationID 是调用方提供的世代标识：进程号与虚拟机世代 ID 的二元组。
// 虚拟机快照恢复或 fork 之后，每个克隆应被赋予不同的 VMGenID。
type GenerationID struct {
	PID     uint64
	VMGenID [16]byte
}

// Options 注入 Generator 所需的全部外部依赖。
type Options struct {
	// Entropy 用新熵填满 p；失败时必须返回错误。
	Entropy func(p []byte) error
	// Generation 返回当前世代标识（进程号，虚拟机世代 ID）。
	Generation func() GenerationID
	// Clock 返回单调时钟读数（纳秒），只允许前进。
	Clock func() int64
}

const (
	keySize     = 32
	ctrSize     = 16
	entropySize = keySize + ctrSize
	chunkSize   = 4096
)

// Generator 是计数器模式（AES-256-CTR）的确定性随机源包装。
// 每次取数前检查世代标识与单调时钟：二元组变化或时钟回退时，
// 先用“旧状态 ‖ 新熵 ‖ 世代标识 ‖ 时钟读数”混合重播种，
// 并丢弃已缓冲但未使用的字节。重播种期间并发调用者阻塞到完成；
// 熵源失败时返回错误，绝不继续输出。可安全并发使用。
type Generator struct {
	mu   sync.Mutex
	opts Options

	key    [keySize]byte
	ctr    [ctrSize]byte
	stream cipher.Stream
	buf    []byte

	lastGen   GenerationID
	lastClock int64
	seeded    bool
	reseeds   uint64
}

var _ io.Reader = (*Generator)(nil)

// NewGenerator 构造 Generator；首次取数时会用熵源完成初始播种。
func NewGenerator(opts Options) (*Generator, error) {
	if opts.Entropy == nil || opts.Generation == nil || opts.Clock == nil {
		return nil, fmt.Errorf("netsec: Options 的 Entropy、Generation、Clock 均不能为空")
	}
	return &Generator{opts: opts}, nil
}

// Read 填满 p。取数前按需重播种；熵源失败时返回错误且 p 不被写入。
func (g *Generator) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gen := g.opts.Generation()
	now := g.opts.Clock()
	if !g.seeded || gen != g.lastGen || now < g.lastClock {
		if err := g.reseedLocked(gen, now); err != nil {
			return 0, err
		}
	}
	g.lastGen = gen
	g.lastClock = now

	total := 0
	for total < len(p) {
		if len(g.buf) == 0 {
			g.refillLocked()
		}
		n := copy(p[total:], g.buf)
		g.buf = g.buf[n:]
		total += n
	}
	return total, nil
}

// Reseeds 返回已完成的重播种次数（含初始播种），用于监控与测试。
func (g *Generator) Reseeds() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reseeds
}

// reseedLocked 用“旧状态 ‖ 新熵 ‖ 世代标识 ‖ 时钟读数”混合出新的密钥与计数器，
// 并丢弃旧密钥流中已缓冲但未使用的字节。调用时必须已持有 g.mu。
func (g *Generator) reseedLocked(gen GenerationID, now int64) error {
	fresh := make([]byte, entropySize)
	if err := g.opts.Entropy(fresh); err != nil {
		return fmt.Errorf("netsec: 熵源读取失败，拒绝继续输出: %w", err)
	}
	defer zeroBytes(fresh)

	h := sha512.New()
	h.Write(g.key[:])
	h.Write(g.ctr[:])
	h.Write(fresh)
	var fixed [8 + 16 + 8]byte
	binary.BigEndian.PutUint64(fixed[0:8], gen.PID)
	copy(fixed[8:24], gen.VMGenID[:])
	binary.BigEndian.PutUint64(fixed[24:32], uint64(now))
	h.Write(fixed[:])
	sum := h.Sum(nil)
	defer zeroBytes(sum)

	copy(g.key[:], sum[:keySize])
	copy(g.ctr[:], sum[keySize:keySize+ctrSize])

	block, err := aes.NewCipher(g.key[:])
	if err != nil {
		return fmt.Errorf("netsec: 初始化计数器模式失败: %w", err)
	}
	g.stream = cipher.NewCTR(block, g.ctr[:])
	g.buf = nil
	g.seeded = true
	g.reseeds++
	return nil
}

// refillLocked 生成下一段密钥流。调用时必须已持有 g.mu。
func (g *Generator) refillLocked() {
	chunk := make([]byte, chunkSize)
	g.stream.XORKeyStream(chunk, chunk)
	g.buf = chunk
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
