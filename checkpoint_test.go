package checkpoint

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempPath(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "checkpoint-test")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "checkpoint.json"), func() { os.RemoveAll(dir) }
}

// TestLowWatermarkAdvance 验证乱序完成时 LowWatermark 只越过连续前缀。
func TestLowWatermarkAdvance(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	m, err := Open(path, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.LowWatermark(); got != 1000 {
		t.Fatalf("initial watermark = %d, want 1000", got)
	}
	// 乱序提交：1002 先完成，水位不能越过 1000。
	m.Begin(1000)
	m.Begin(1001)
	m.Begin(1002)
	if err := m.Commit(1002); err != nil {
		t.Fatal(err)
	}
	if got := m.LowWatermark(); got != 1000 {
		t.Fatalf("watermark after out-of-order commit = %d, want 1000", got)
	}
	if err := m.Commit(1000); err != nil {
		t.Fatal(err)
	}
	if got := m.LowWatermark(); got != 1001 {
		t.Fatalf("watermark = %d, want 1001", got)
	}
	// 补齐 1001 后，水位应连续越过已完成的 1002。
	if err := m.Commit(1001); err != nil {
		t.Fatal(err)
	}
	if got := m.LowWatermark(); got != 1003 {
		t.Fatalf("watermark = %d, want 1003", got)
	}
}

// TestCrashRecovery 验收场景：
// 10 个工作线程并行消费位点 1000..2000，线程 3 卡在 1050；
// 系统崩溃重启后，进度读取器必须返回 1050 作为安全重放起始点。
func TestCrashRecovery(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	const (
		base    = 1000
		end     = 2000 // 含
		workers = 10
		slow    = 3    // 卡住的线程编号
		stuckAt = 1050 // 线程 3 卡住未完成的位点
	)

	m, err := Open(path, base)
	if err != nil {
		t.Fatal(err)
	}

	// 将 [1000,2000] 切成 10 个连续块，块 c 分给线程 (c+3)%10，
	// 因此线程 3 拿到第一块 [1000,1100)，位点 1050 由线程 3 负责。
	chunks := make([][2]uint64, workers)
	chunkSize := uint64((end - base + 1) / workers)
	for c := 0; c < workers; c++ {
		lo := uint64(base) + uint64(c)*chunkSize
		hi := lo + chunkSize
		if c == workers-1 {
			hi = end + 1
		}
		chunks[c] = [2]uint64{lo, hi}
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		chunk := chunks[(w+workers-slow)%workers] // 线程 slow 拿 chunk 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pos := chunk[0]; pos < chunk[1]; pos++ {
				if !m.Begin(pos) {
					continue
				}
				if w == slow && pos >= stuckAt {
					// 线程 3 处理缓慢：卡在 1050（及其后的位点），
					// 已 Begin 但未 Commit，模拟崩溃前的悬挂状态。
					return
				}
				if err := m.Commit(pos); err != nil {
					t.Errorf("commit %d: %v", pos, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 崩溃前：水位应停在 1050（1000..1049 已连续完成）。
	if got := m.LowWatermark(); got != stuckAt {
		t.Fatalf("watermark before crash = %d, want %d", got, stuckAt)
	}

	// 模拟系统崩溃：不执行任何清理，直接丢弃内存中的 Manager。
	m = nil

	// 重启：进度读取器从磁盘恢复安全重放起始点。
	resume, err := LoadResumePoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if resume != stuckAt {
		t.Fatalf("resume point after crash = %d, want %d", resume, stuckAt)
	}

	// 续跑：从 1050 重放到 2000，保证不漏掉任何未完成事件。
	m2, err := Open(path, base)
	if err != nil {
		t.Fatal(err)
	}
	for pos := resume; pos <= end; pos++ {
		if !m2.Begin(pos) {
			continue
		}
		if err := m2.Commit(pos); err != nil {
			t.Fatal(err)
		}
	}
	if got := m2.LowWatermark(); got != end+1 {
		t.Fatalf("final watermark = %d, want %d", got, end+1)
	}
}

// TestConcurrentCommit 多线程并发提交，验证水位最终恰好越过全部位点。
func TestConcurrentCommit(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	const (
		base    = 0
		n       = 5000
		workers = 16
	)
	m, err := Open(path, base)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pos := uint64(w); pos < n; pos += workers {
				if m.Begin(pos) {
					if err := m.Commit(pos); err != nil {
						t.Errorf("commit %d: %v", pos, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := m.LowWatermark(); got != n {
		t.Fatalf("watermark = %d, want %d", got, n)
	}
	resume, err := LoadResumePoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if resume != n {
		t.Fatalf("resume point = %d, want %d", resume, n)
	}
}

// TestCorruptCheckpoint 检查点损坏时必须报错而不是静默回退。
func TestCorruptCheckpoint(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	if err := ioutil.WriteFile(path, []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResumePoint(path); err == nil {
		t.Fatal("expected error for corrupt checkpoint, got nil")
	}
	if _, err := Open(path, 0); err == nil {
		t.Fatal("expected error for corrupt checkpoint, got nil")
	}
}
