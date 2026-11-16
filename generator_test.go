package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// replayEntropy 是确定性熵源：两个实例的输出序列完全相同，
// 模拟两个克隆“共享同一熵源输出回放”。
type replayEntropy struct {
	counter uint64
	calls   int
	failErr error
}

func (e *replayEntropy) fill(p []byte) error {
	e.calls++
	if e.failErr != nil {
		return e.failErr
	}
	for len(p) > 0 {
		var ctr [8]byte
		binary.BigEndian.PutUint64(ctr[:], e.counter)
		e.counter++
		block := sha256.Sum256(append([]byte("netsec-test-entropy"), ctr[:]...))
		n := copy(p, block[:])
		p = p[n:]
	}
	return nil
}

type manualClock struct{ v atomic.Int64 }

func (c *manualClock) now() int64 { return c.v.Load() }

// atomicGen 持有当前世代标识，模拟恢复后由 hypervisor 更新 VMGenID。
type atomicGen struct{ v atomic.Value }

func newAtomicGen(g GenerationID) *atomicGen {
	ag := &atomicGen{}
	ag.v.Store(g)
	return ag
}

func (ag *atomicGen) get() GenerationID { return ag.v.Load().(GenerationID) }

func (ag *atomicGen) set(g GenerationID) { ag.v.Store(g) }

func mustRead(t *testing.T, g *Generator, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := g.Read(buf); err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	return buf
}

func genID(pid uint64, tag byte) GenerationID {
	var g GenerationID
	g.PID = pid
	g.VMGenID[0] = tag
	return g
}

// 快照恢复后两个克隆世代标识分化，即使熵源输出被完整回放，
// 两份令牌流也必须互不重叠。
func TestCloneOutputsDivergeAfterGenerationChange(t *testing.T) {
	clkA, clkB := &manualClock{}, &manualClock{}
	clkA.v.Store(1000)
	clkB.v.Store(1000)
	genA := newAtomicGen(genID(4242, 0x0A))
	genB := newAtomicGen(genID(4242, 0x0A))

	a, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: genA.get, Clock: clkA.now})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: genB.get, Clock: clkB.now})
	if err != nil {
		t.Fatal(err)
	}

	// 恢复前：世代相同、熵回放相同、时钟相同，输出完全一致（复现漏洞前提）。
	if ta, tb := mustRead(t, a, 32), mustRead(t, b, 32); !bytes.Equal(ta, tb) {
		t.Fatal("测试前提不成立：相同世代与熵回放下的输出应当一致")
	}

	// 快照恢复：两个克隆获得不同的 VMGenID。
	genA.set(genID(4242, 0xA1))
	genB.set(genID(4242, 0xB2))

	const tokens = 1024
	setA := make(map[[32]byte]struct{}, tokens)
	for i := 0; i < tokens; i++ {
		var tok [32]byte
		copy(tok[:], mustRead(t, a, 32))
		if _, dup := setA[tok]; dup {
			t.Fatalf("克隆 A 内部出现重复令牌 #%d", i)
		}
		setA[tok] = struct{}{}
	}
	for i := 0; i < tokens; i++ {
		var tok [32]byte
		copy(tok[:], mustRead(t, b, 32))
		if _, dup := setA[tok]; dup {
			t.Fatalf("克隆 B 的令牌 #%d 与克隆 A 重叠", i)
		}
		setA[tok] = struct{}{}
	}
}

// 时钟回退必须触发重播种；时钟原地不动或前进则不触发。
func TestClockRollbackTriggersReseed(t *testing.T) {
	clk := &manualClock{}
	clk.v.Store(5000)
	gen := newAtomicGen(genID(7, 0x01))
	g, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: gen.get, Clock: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	mustRead(t, g, 16)
	if got := g.Reseeds(); got != 1 {
		t.Fatalf("初始播种后 Reseeds = %d, 期望 1", got)
	}

	clk.v.Store(6000) // 前进：不触发
	mustRead(t, g, 16)
	clk.v.Store(6000) // 原地不动：不触发
	mustRead(t, g, 16)
	if got := g.Reseeds(); got != 1 {
		t.Fatalf("时钟前进/不动后 Reseeds = %d, 期望 1", got)
	}

	clk.v.Store(5999) // 回退 1ns：必须触发
	mustRead(t, g, 16)
	if got := g.Reseeds(); got != 2 {
		t.Fatalf("时钟回退后 Reseeds = %d, 期望 2", got)
	}
}

// 熵源失败时 Read 必须返回错误、不写入任何输出，且旧状态保持可恢复。
func TestEntropyFailureReturnsError(t *testing.T) {
	src := &replayEntropy{}
	clk := &manualClock{}
	clk.v.Store(100)
	gen := newAtomicGen(genID(9, 0x01))
	g, err := NewGenerator(Options{Entropy: src.fill, Generation: gen.get, Clock: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	mustRead(t, g, 32) // 初始播种成功

	src.failErr = errors.New("entropy unavailable")
	gen.set(genID(9, 0x02)) // 世代变化触发重播种，但熵源失败
	out := make([]byte, 32)
	if _, err := g.Read(out); err == nil {
		t.Fatal("熵源失败时 Read 应返回错误")
	}
	if !bytes.Equal(out, make([]byte, 32)) {
		t.Fatal("熵源失败时 Read 不应写入任何输出")
	}

	src.failErr = nil // 熵源恢复后旧状态仍可完成重播种
	tok := mustRead(t, g, 32)
	if bytes.Equal(tok, make([]byte, 32)) {
		t.Fatal("熵源恢复后应能正常输出")
	}
	if got := g.Reseeds(); got != 2 {
		t.Fatalf("恢复后 Reseeds = %d, 期望 2", got)
	}
}

// 重播种必须丢弃已缓冲但未使用的字节：重播种后的输出是新密钥流的起点，
// 而不是旧缓冲区的延续。
func TestBufferedBytesDiscardedOnReseed(t *testing.T) {
	clk := &manualClock{}
	clk.v.Store(42)
	gen := newAtomicGen(genID(1, 0x01))
	g, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: gen.get, Clock: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	mustRead(t, g, 1) // 缓冲 4096 字节，仅用 1 字节

	// 双胞胎：不重播种，继续读到的就是旧缓冲区的后续字节。
	twinGen := newAtomicGen(genID(1, 0x01))
	twin, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: twinGen.get, Clock: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	mustRead(t, twin, 1)
	twinNext := mustRead(t, twin, 32)

	// 对照组：经历与 g 完全相同的熵消耗与世代变化，直接取新密钥流。
	ctrlGen := newAtomicGen(genID(1, 0x01))
	ctrl, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: ctrlGen.get, Clock: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	mustRead(t, ctrl, 1)
	ctrlGen.set(genID(1, 0x02))
	ctrlNext := mustRead(t, ctrl, 32)

	gen.set(genID(1, 0x02)) // 触发 g 重播种
	got := mustRead(t, g, 32)

	if bytes.Equal(got, twinNext) {
		t.Fatal("重播种后输出了旧缓冲区的残留字节")
	}
	if !bytes.Equal(got, ctrlNext) {
		t.Fatal("重播种后输出应等于新种子密钥流的起点")
	}
	if got := g.Reseeds(); got != 2 {
		t.Fatalf("Reseeds = %d, 期望 2", got)
	}
}

// 两个克隆并发取数：世代在并发期间分化，重播种期间调用者阻塞，
// 最终两份输出互不重叠且各自无重复。
func TestConcurrentClonesNoOverlap(t *testing.T) {
	clkA, clkB := &manualClock{}, &manualClock{}
	genA := newAtomicGen(genID(100, 0x0A))
	genB := newAtomicGen(genID(100, 0x0A))

	a, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: genA.get, Clock: clkA.now})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewGenerator(Options{Entropy: (&replayEntropy{}).fill, Generation: genB.get, Clock: clkB.now})
	if err != nil {
		t.Fatal(err)
	}

	mustRead(t, a, 32) // 初始播种，世代相同
	mustRead(t, b, 32)

	// 并发取数前分化世代：每个克隆的第一次并发读将阻塞完成重播种。
	genA.set(genID(100, 0xA1))
	genB.set(genID(100, 0xB2))

	const (
		workers       = 16
		tokensPerRead = 16
		tokenSize     = 32
	)
	collect := func(g *Generator, out *[][]byte, wg *sync.WaitGroup) {
		defer wg.Done()
		var local [][]byte
		for i := 0; i < tokensPerRead; i++ {
			buf := make([]byte, tokenSize)
			if _, err := g.Read(buf); err != nil {
				t.Errorf("并发 Read 失败: %v", err)
				return
			}
			local = append(local, buf)
		}
		*out = local
	}

	var wg sync.WaitGroup
	results := make([][][]byte, 2*workers)
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go collect(a, &results[2*i], &wg)
		go collect(b, &results[2*i+1], &wg)
	}
	wg.Wait()

	seen := make(map[[32]byte]struct{}, 2*workers*tokensPerRead)
	total := 0
	for _, batch := range results {
		for _, tok := range batch {
			var key [32]byte
			copy(key[:], tok)
			if _, dup := seen[key]; dup {
				t.Fatal("并发输出中出现重复令牌（克隆间或克隆内重叠）")
			}
			seen[key] = struct{}{}
			total++
		}
	}
	if want := 2 * workers * tokensPerRead; total != want {
		t.Fatalf("共收集 %d 个令牌, 期望 %d", total, want)
	}
	if got := a.Reseeds(); got != 2 {
		t.Fatalf("克隆 A Reseeds = %d, 期望 2（初始 + 世代变化）", got)
	}
	if got := b.Reseeds(); got != 2 {
		t.Fatalf("克隆 B Reseeds = %d, 期望 2（初始 + 世代变化）", got)
	}
}
