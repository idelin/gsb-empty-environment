package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Page 是共享页的字节级访问抽象。
//
// 生产环境可由 mmap 区域实现；测试环境使用加锁的内存页，便于在读取器的
// 两次读取之间确定性地改写内容。方法语义与 io.ReaderAt/WriterAt 相同：
// 必须读满/写满 len(p) 个字节，否则返回错误。
type Page interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Size() int64
}

// MemPage 是加锁的内存共享页，供测试与非 mmap 场景使用。
type MemPage struct {
	mu  sync.Mutex
	mem []byte
}

// NewMemPage 创建给定字节数的零值内存页。
func NewMemPage(size int) *MemPage {
	if size <= headerSize {
		panic("netsec: page too small to hold header")
	}
	return &MemPage{mem: make([]byte, size)}
}

func (m *MemPage) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readAtLocked(p, off)
}

func (m *MemPage) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeAtLocked(p, off)
}

func (m *MemPage) Size() int64 { return int64(len(m.mem)) }

// Lock/Unlock 供受控写入器在一次多步发布过程中持有页锁，
// 使 seqlock 的“写入中”窗口可被精确控制。
func (m *MemPage) Lock()   { m.mu.Lock() }
func (m *MemPage) Unlock() { m.mu.Unlock() }

// BytesLocked 返回底层切片，调用方必须持有锁。仅供同包写入器与测试使用。
func (m *MemPage) BytesLocked() []byte { return m.mem }

func (m *MemPage) readAtLocked(p []byte, off int64) (int, error) {
	return readAtLocked(m.mem, p, off)
}

func (m *MemPage) writeAtLocked(p []byte, off int64) (int, error) {
	return writeAtLocked(m.mem, p, off)
}

func readAtLocked(mem, p []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(mem)) {
		return 0, io.EOF
	}
	n := copy(p, mem[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func writeAtLocked(mem, p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(mem)) || uint64(off)+uint64(len(p)) > uint64(len(mem)) {
		return 0, io.ErrShortWrite
	}
	return copy(mem[off:], p), nil
}

// readPageAt 从 Page 的 off 偏移处读满 len(dst) 个字节。
func readPageAt(page Page, dst []byte, off int64) error {
	_, err := io.ReadFull(io.NewSectionReader(page, off, int64(len(dst))), dst)
	return err
}

// 读取器返回的哨兵错误。
var (
	// ErrUncertain 表示在预算内无法取得两个一致的稳定快照，调用方应丢弃本轮结果。
	ErrUncertain = errors.New("netsec: read did not stabilize within retry budget")
	// ErrDigestMismatch 表示两个一致快照的摘要均与载荷不符（确定的损坏/伪造）。
	ErrDigestMismatch = errors.New("netsec: record digest mismatch")
)

// ReaderConfig 配置读取器。
type ReaderConfig struct {
	// MaxAttempts 是最多采样的快照数。默认 64。
	MaxAttempts int
	// MaxPayload 是可接受的最大记录长度与容量上限。默认 1 MiB。
	MaxPayload uint32
	// BetweenSamples 在每次采样之后被调用（若设置），仅供测试注入并发改写。
	BetweenSamples func()
}

// Reader 是采集宿主侧的 seqlock 读取器，可被多个 goroutine 并发使用。
type Reader struct {
	page Page
	cfg  ReaderConfig
}

// NewReader 创建读取器。
func NewReader(page Page, cfg ReaderConfig) *Reader {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 64
	}
	if cfg.MaxPayload == 0 {
		cfg.MaxPayload = 1 << 20
	}
	return &Reader{page: page, cfg: cfg}
}

// snapshot 是一次采样窗口内的全部结果。长度、字段、摘要、序号与代次
// 必须在同一窗口取得；窗口两端 (gen, seq) 相同且 seq 为偶数才算稳定。
type snapshot struct {
	gen uint64
	seq uint64
	cap uint32
	len uint32
	sum [digestSize]byte

	data []byte

	// transient 为 true 表示窗口内观测到并发写入，应重试。
	transient bool
	// structural 非空表示稳定复现的结构问题（可被 errors.Is 识别为 ErrMalformed）。
	structural error
	// digestBad 表示结构合法但摘要与载荷不符。
	digestBad bool
}

func (s *snapshot) equal(other *snapshot) bool {
	return s.gen == other.gen &&
		s.seq == other.seq &&
		s.cap == other.cap &&
		s.len == other.len &&
		s.sum == other.sum &&
		bytes.Equal(s.data, other.data) &&
		s.digestBad == other.digestBad &&
		errors.Is(s.structural, ErrMalformed) == errors.Is(other.structural, ErrMalformed)
}

// Read 执行带预算的 seqlock 读取。
//
// 成功时返回的 Record 保证：长度、字段与摘要来自同一个稳定快照，该快照
// 序列号为偶数，且与紧邻的另一次稳定快照完全一致。重试耗尽返回
// ErrUncertain；稳定复现的结构损坏返回 ErrMalformed，稳定复现的摘要
// 不符返回 ErrDigestMismatch。
func (r *Reader) Read(ctx context.Context) (*Record, error) {
	var prev *snapshot

	for attempt := 0; attempt < r.cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		cur, err := r.readSnapshot()
		if err != nil {
			return nil, err
		}
		if r.cfg.BetweenSamples != nil {
			r.cfg.BetweenSamples()
		}

		// 写入进行中、版本回绕、窗口内被改写：瞬时状态，重置配对并重试。
		if cur.transient {
			prev = nil
			continue
		}

		if prev != nil && cur.equal(prev) {
			if cur.structural != nil {
				return nil, cur.structural
			}
			if cur.digestBad {
				return nil, ErrDigestMismatch
			}
			return ParseRecord(cur.data)
		}
		prev = cur
	}

	return nil, ErrUncertain
}

// readSnapshot 完成一次 seqlock 采样：读版本 -> 读全部数据 -> 再读版本。
func (r *Reader) readSnapshot() (*snapshot, error) {
	s := &snapshot{}

	genBefore, seqBefore, even := r.readVersion()
	if !even {
		s.transient = true
		return s, nil
	}

	// 整个头部在一次 ReadAt 内取回。一次快照的底层读取序列固定为：
	// 版本 -> 头部 -> 载荷（长度为 0 时跳过）-> 版本。
	hdr := make([]byte, headerSize)
	if err := readPageAt(r.page, hdr, 0); err != nil {
		return nil, err
	}

	// 版本以窗口两端的独立读取为准；头部内联版本仅用于撕裂检测。
	hdrGen := le.Uint64(hdr[offGen:])
	hdrSeq := le.Uint64(hdr[offSeq:])
	if le.Uint32(hdr[offMagic:]) != magic || le.Uint32(hdr[offVersion:]) != version {
		s.structural = malformedf("unrecognized shared page header")
	} else if hdrGen != genBefore || hdrSeq != seqBefore {
		// 头部分片在读取过程中被撕裂。
		s.transient = true
		return s, nil
	} else {
		s.gen, s.seq = genBefore, seqBefore
		s.cap = le.Uint32(hdr[offCapacity:])
		s.len = le.Uint32(hdr[offLength:])
		copy(s.sum[:], hdr[offDigest:offDigest+digestSize])
	}

	if s.structural == nil {
		switch {
		case s.cap == 0 || uint64(s.cap) > uint64(r.cfg.MaxPayload):
			s.structural = malformedf("capacity %d out of allowed range", s.cap)
		case s.len > s.cap:
			s.structural = malformedf("record length %d exceeds capacity %d", s.len, s.cap)
		case uint64(PageSize(s.cap)) > uint64(r.page.Size()):
			s.structural = malformedf("capacity exceeds mapped page")
		}
	}

	if s.structural == nil {
		s.data = make([]byte, s.len)
		if err := readPageAt(r.page, s.data, headerSize); err != nil {
			return nil, err
		}
		s.digestBad = digestPayload(s.data) != s.sum
	}

	genAfter, seqAfter, even := r.readVersion()
	if !even {
		s.transient = true
		return s, nil
	}
	if genAfter != genBefore || seqAfter != seqBefore {
		// 窗口内发生了发布或回绕：长度/字段/摘要可能跨越了两个版本。
		s.transient = true
		return s, nil
	}
	return s, nil
}

// readVersion 读取 (gen, seq)；seq 为奇数时 even=false。
func (r *Reader) readVersion() (gen, seq uint64, even bool) {
	buf := make([]byte, 16)
	if err := readPageAt(r.page, buf, offGen); err != nil {
		return 0, 0, false
	}
	gen = le.Uint64(buf[:8])
	seq = le.Uint64(buf[8:])
	return gen, seq, seq&1 == 0
}

// malformedError 让 errors.Is(err, ErrMalformed) 成立。
type malformedError struct{ msg string }

func malformedf(format string, args ...any) error {
	return &malformedError{msg: fmt.Sprintf(format, args...)}
}

func (e *malformedError) Error() string { return ErrMalformed.Error() + ": " + e.msg }
func (e *malformedError) Is(target error) bool {
	return target == ErrMalformed
}

var _ error = (*malformedError)(nil)
