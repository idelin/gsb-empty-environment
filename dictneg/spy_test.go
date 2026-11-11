package dictneg

import (
	"sync"
)

// spyCodec 记录每个解压器实际使用的字典摘要，用于断言“绝不猜测解码”：
// 帧只能由其绑定摘要对应的解压器处理。
type spyCodec struct {
	mu       sync.Mutex
	openBy   map[Digest]int
	decodeBy map[Digest]int
}

func newSpyCodec() *spyCodec {
	return &spyCodec{
		openBy:   map[Digest]int{},
		decodeBy: map[Digest]int{},
	}
}

func (s *spyCodec) Open(content []byte) (Decompressor, error) {
	d := SumDigest(content)
	s.mu.Lock()
	s.openBy[d]++
	s.mu.Unlock()
	return &spyDecompressor{codec: s, digest: d}, nil
}

func (s *spyCodec) opens(d Digest) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openBy[d]
}

func (s *spyCodec) decodes(d Digest) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decodeBy[d]
}

type spyDecompressor struct {
	codec  *spyCodec
	digest Digest
}

func (d *spyDecompressor) Decompress(payload []byte) ([]byte, error) {
	d.codec.mu.Lock()
	d.codec.decodeBy[d.digest]++
	d.codec.mu.Unlock()
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

// deliveredFrame 是 sink 收到的一帧解压结果。
type deliveredFrame struct {
	digest Digest
	seq    uint64
	plain  []byte
}

func newSink() (*[]deliveredFrame, Sink) {
	var got []deliveredFrame
	sink := func(d Digest, seq uint64, plain []byte) {
		got = append(got, deliveredFrame{digest: d, seq: seq, plain: append([]byte(nil), plain...)})
	}
	return &got, sink
}
