package netsec

import "io"

// rawPage 是无锁内存页：它不做任何并发保护，调用方必须像真实共享内存
// 那样通过 seqlock 协议（偶数序号 + 双快照）来保证一致性。
//
// 它让测试可以在“写入窗口保持打开（奇数序号）”期间继续执行读取器，
// 而不会被 MemPage 的互斥锁挡住。
type rawPage struct {
	mem []byte
}

func newRawPage(size int) *rawPage { return &rawPage{mem: make([]byte, size)} }

func (p *rawPage) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(p.mem)) {
		return 0, io.EOF
	}
	n := copy(b, p.mem[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

func (p *rawPage) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(p.mem)) || uint64(off)+uint64(len(b)) > uint64(len(p.mem)) {
		return 0, io.ErrShortWrite
	}
	return copy(p.mem[off:], b), nil
}

func (p *rawPage) Size() int64 { return int64(len(p.mem)) }

// initHeader 在无锁页上初始化共享页头部。
func (p *rawPage) initHeader(capacity uint32) {
	le.PutUint32(p.mem[offMagic:], magic)
	le.PutUint32(p.mem[offVersion:], version)
	le.PutUint64(p.mem[offGen:], 0)
	le.PutUint64(p.mem[offSeq:], 0)
	le.PutUint32(p.mem[offCapacity:], capacity)
	le.PutUint32(p.mem[offLength:], 0)
	sum := digestPayload(nil)
	copy(p.mem[offDigest:offDigest+digestSize], sum[:])
}

func (p *rawPage) setSeq(gen, seq uint64) {
	le.PutUint64(p.mem[offGen:], gen)
	le.PutUint64(p.mem[offSeq:], seq)
}

func (p *rawPage) putRecord(payload []byte) {
	recordArea := p.mem[headerSize:]
	for i := range recordArea {
		recordArea[i] = 0
	}
	copy(recordArea, payload)
	sum := digestPayload(payload)
	copy(p.mem[offDigest:offDigest+digestSize], sum[:])
	le.PutUint32(p.mem[offLength:], uint32(len(payload)))
}
