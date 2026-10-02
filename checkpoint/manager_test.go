package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	acceptStart   = 1000
	acceptEnd     = 2000 // 闭区间 [1000, 2000]
	acceptWorkers = 10
	stuckPos      = 1050 // 线程 3 卡住的位点
)

// TestAcceptance_CrashResumeAtStuckPosition 验收场景：
// 10 个工作线程并行消费位点 1000..2000；线程 3 处理较慢卡在 1050；
// 系统崩溃重启后，进度读取器必须返回 1050 作为安全重放起始点。
func TestAcceptance_CrashResumeAtStuckPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.dat")

	mgr, err := Open(path, acceptStart)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// 派发器：1050 固定派给线程 3（其处理较慢），其余位点由所有
	// 线程从共享队列动态拉取，贴近真实消费端。
	jobs := make(chan uint64)
	stuckJob := make(chan uint64, 1)
	go func() {
		for pos := uint64(acceptStart); pos <= acceptEnd; pos++ {
			if pos == stuckPos {
				stuckJob <- pos
				continue
			}
			jobs <- pos
		}
	}()

	stuckGate := make(chan struct{}) // 关闭后线程 3 才会放行 1050
	var wg sync.WaitGroup
	for w := 0; w < acceptWorkers; w++ {
		wg.Add(1)
		go func(id int, m *Manager) {
			defer wg.Done()
			if id == 3 {
				pos := <-stuckJob // 线程 3 领到 1050
				<-stuckGate       // 处理较慢，卡在 1050
				if err := m.Ack(pos); err != nil {
					t.Errorf("Ack(%d): %v", pos, err)
				}
			}
			for pos := range jobs {
				if err := m.Ack(pos); err != nil {
					t.Errorf("Ack(%d): %v", pos, err)
				}
			}
		}(w, mgr)
	}

	// 等待除 1050 外的所有位点被消费完成：水位推进到 1050 后，
	// 因线程 3 的缺口无法继续越过。
	deadline := time.Now().Add(10 * time.Second)
	for mgr.LowWatermark() != stuckPos {
		if time.Now().After(deadline) {
			t.Fatalf("超时：水位停在 %d，期望推进到 %d", mgr.LowWatermark(), stuckPos)
		}
		time.Sleep(time.Millisecond)
	}
	// 再确认水位稳定（其余 Ack 全部到齐后仍停在 1050）。
	time.Sleep(50 * time.Millisecond)
	if got := mgr.LowWatermark(); got != stuckPos {
		t.Fatalf("水位 = %d，期望稳定在 %d", got, stuckPos)
	}

	// 模拟系统崩溃：不做任何清理，直接丢弃 mgr（goroutine 随进程退出）。
	// 崩溃瞬间水位已同步落盘。
	mgr = nil

	// 重启：独立进度读取器读取安全重放起始点。
	resume, err := ReadCheckpoint(path, acceptStart)
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}
	if resume != stuckPos {
		t.Fatalf("崩溃恢复后安全重放起始点 = %d，期望 %d", resume, stuckPos)
	}

	// 重启后从水位平滑续跑：重放 [1050, 2000]，1050 不再丢失。
	mgr2, err := Open(path, acceptStart)
	if err != nil {
		t.Fatalf("重启后 Open: %v", err)
	}
	if got := mgr2.LowWatermark(); got != stuckPos {
		t.Fatalf("重启后低水位 = %d，期望 %d", got, stuckPos)
	}
	for pos := resume; pos <= acceptEnd; pos++ {
		if err := mgr2.Ack(pos); err != nil {
			t.Fatalf("续跑 Ack(%d): %v", pos, err)
		}
	}
	if got := mgr2.LowWatermark(); got != acceptEnd+1 {
		t.Fatalf("续跑完成后水位 = %d，期望 %d", got, acceptEnd+1)
	}

	close(stuckGate)
	close(jobs)
	wg.Wait()
}

// TestLowWatermark_OutOfOrderAcks 乱序 Ack 时水位只能连续推进。
func TestLowWatermark_OutOfOrderAcks(t *testing.T) {
	mgr, err := Open("", 100)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, pos := range []uint64{103, 101, 104, 100} {
		if err := mgr.Ack(pos); err != nil {
			t.Fatalf("Ack(%d): %v", pos, err)
		}
	}
	// 102 未 Ack，水位停在 102。
	if got := mgr.LowWatermark(); got != 102 {
		t.Fatalf("水位 = %d，期望 102", got)
	}
	// 缺口 102 未补齐，后续 Ack 不能推进水位。
	if err := mgr.Ack(106); err != nil {
		t.Fatalf("Ack(106): %v", err)
	}
	if got := mgr.LowWatermark(); got != 102 {
		t.Fatalf("存在缺口时水位 = %d，期望 102", got)
	}
	// 补齐 102 后水位连续越过 103、104，停在缺口 105。
	if err := mgr.Ack(102); err != nil {
		t.Fatalf("Ack(102): %v", err)
	}
	if got := mgr.LowWatermark(); got != 105 {
		t.Fatalf("补齐缺口后水位 = %d，期望 105", got)
	}
	// 补齐 105 后连同 106 一起越过。
	if err := mgr.Ack(105); err != nil {
		t.Fatalf("Ack(105): %v", err)
	}
	if got := mgr.LowWatermark(); got != 107 {
		t.Fatalf("水位 = %d，期望 107", got)
	}
}

// TestAck_Idempotent 重复 Ack 与低于水位的 Ack 均为无操作。
func TestAck_Idempotent(t *testing.T) {
	mgr, err := Open("", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		for pos := uint64(0); pos < 5; pos++ {
			if err := mgr.Ack(pos); err != nil {
				t.Fatalf("Ack(%d): %v", pos, err)
			}
		}
	}
	if got := mgr.LowWatermark(); got != 5 {
		t.Fatalf("水位 = %d，期望 5", got)
	}
	if err := mgr.Ack(2); err != nil { // 已低于水位
		t.Fatalf("Ack(2): %v", err)
	}
	if got := mgr.LowWatermark(); got != 5 {
		t.Fatalf("水位 = %d，期望 5", got)
	}
}

// TestPersistence_AtomicFile 持久化文件内容正确且可被新实例恢复。
func TestPersistence_AtomicFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.dat")
	mgr, err := Open(path, 1000)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for pos := uint64(1000); pos < 1010; pos++ {
		if err := mgr.Ack(pos); err != nil {
			t.Fatalf("Ack(%d): %v", pos, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取持久化文件: %v", err)
	}
	if string(data) != "1010\n" {
		t.Fatalf("持久化内容 = %q，期望 %q", data, "1010\n")
	}
	mgr2, err := Open(path, 1000)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	if got := mgr2.LowWatermark(); got != 1010 {
		t.Fatalf("恢复水位 = %d，期望 1010", got)
	}
}

// TestReadCheckpoint_Fallback 文件不存在时读取器返回 fallback。
func TestReadCheckpoint_Fallback(t *testing.T) {
	got, err := ReadCheckpoint(filepath.Join(t.TempDir(), "missing.dat"), 42)
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}
	if got != 42 {
		t.Fatalf("fallback = %d，期望 42", got)
	}
}

// TestCorruptFile 损坏的持久化文件必须报错而不是静默返回错误水位。
func TestCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.dat")
	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Open(path, 0); err == nil {
		t.Fatal("Open 对损坏文件未报错")
	}
	if _, err := ReadCheckpoint(path, 0); err == nil {
		t.Fatal("ReadCheckpoint 对损坏文件未报错")
	}
}

// TestConcurrentAckStress 多 goroutine 随机顺序 Ack 后水位必须精确等于 N。
func TestConcurrentAckStress(t *testing.T) {
	const n = 5000
	mgr, err := Open("", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for pos := uint64(id); pos < n; pos += 16 {
				if err := mgr.Ack(pos); err != nil {
					t.Errorf("Ack(%d): %v", pos, err)
				}
			}
		}(w)
	}
	wg.Wait()
	if got := mgr.LowWatermark(); got != n {
		t.Fatalf("水位 = %d，期望 %d", got, n)
	}
}

// Example 演示：消费 -> 崩溃 -> 从低水位续跑。
func Example() {
	dir, _ := os.MkdirTemp("", "ckpt")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.dat")

	mgr, _ := Open(path, 1000)
	mgr.Ack(1000)
	mgr.Ack(1001)
	mgr.Ack(1003) // 1002 尚未完成，水位停在 1002
	fmt.Println("崩溃前低水位:", mgr.LowWatermark())

	// 崩溃重启后
	resume, _ := ReadCheckpoint(path, 1000)
	fmt.Println("安全重放起始点:", resume)
	// Output:
	// 崩溃前低水位: 1002
	// 安全重放起始点: 1002
}
