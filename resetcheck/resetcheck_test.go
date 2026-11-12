package resetcheck_test

import (
	"fmt"
	"strings"
	"testing"

	"example.org/netsec/q0424/resetcheck"
)

// lockThreshold 是隐藏计数器的锁定阈值：累计认证次数超过它即锁定。
const lockThreshold = 3

// leakyDevice 是带隐藏持久状态的假被测设备：persistentAttempts 计数器
// 在 Reset 后残留，且对快照不可见，模拟“声称复位完成但影响下一次试验”。
type leakyDevice struct {
	persistentAttempts int // 隐藏状态：Reset 与 Restore 都不会清除
	sessionProbes      int // 会话状态：Reset 清除、快照可恢复
}

func (d *leakyDevice) Reset() error {
	d.sessionProbes = 0
	return nil
}

func (d *leakyDevice) Observe(point string) (string, bool) {
	switch point {
	case "welcome":
		return "WELCOME/1.0", true // 欢迎响应恒定，掩盖残留状态
	case "session":
		return fmt.Sprintf("session=%d", d.sessionProbes), true
	case "attempts":
		return fmt.Sprintf("attempts=%d", d.persistentAttempts), true
	}
	return "", false
}

func (d *leakyDevice) Probe(input string) (string, bool) {
	d.sessionProbes++
	switch input {
	case "auth":
		d.persistentAttempts++
		if d.persistentAttempts > lockThreshold {
			return "LOCKED", true
		}
		return "OK", true
	case "ping":
		return "PONG", true
	case "exotic":
		return "", false // 未知响应，但仍消耗会话状态
	}
	return "", false
}

// Snapshot 只捕获可见会话状态；隐藏计数器随之漏网。
func (d *leakyDevice) Snapshot() (any, error) { return d.sessionProbes, nil }

func (d *leakyDevice) Restore(state any) error {
	d.sessionProbes = state.(int)
	return nil
}

// cleanDevice 在 Reset 时真正清除计数器，作为对照组。
type cleanDevice struct{ leakyDevice }

func (d *cleanDevice) Reset() error {
	d.persistentAttempts = 0
	d.sessionProbes = 0
	return nil
}

// TestWelcomeOnlyCannotDetectResidual 验证只比较一次欢迎响应会漏检：
// 隐藏计数器已影响后续试验，但每次复位后的欢迎响应完全相同。
func TestWelcomeOnlyCannotDetectResidual(t *testing.T) {
	dev := &leakyDevice{persistentAttempts: lockThreshold + 1}
	res, err := resetcheck.Check(dev, resetcheck.Config{
		Observations: []string{"welcome"},
		Resets:       4,
	})
	if err != nil {
		t.Fatalf("Check 出错: %v", err)
	}
	if res != nil {
		t.Fatalf("仅比较欢迎响应不应发现差异，却得到: %v", res)
	}
}

// TestBehaviorTreeDetectsResidualCounter 验证行为树比较能发现残留计数器，
// 并返回最短探测序列与两个复位历史。
func TestBehaviorTreeDetectsResidualCounter(t *testing.T) {
	dev := &leakyDevice{}
	res, err := resetcheck.Check(dev, resetcheck.Config{
		Observations: []string{"welcome"},
		Sequences:    [][]string{{"auth", "auth"}, {"ping"}},
		Resets:       4,
	})
	if err != nil {
		t.Fatalf("Check 出错: %v", err)
	}
	if res == nil {
		t.Fatal("行为树比较应发现残留计数器导致的差异")
	}
	t.Logf("分歧:\n%s", res)

	wantPath := []string{"auth", "auth"}
	if strings.Join(res.Path, ",") != strings.Join(wantPath, ",") {
		t.Fatalf("最短探测序列 = %v，期望 %v", res.Path, wantPath)
	}
	if res.A.Reset != 0 || res.B.Reset != 1 {
		t.Fatalf("复位历史来自复位 %d 与 %d，期望 0 与 1", res.A.Reset, res.B.Reset)
	}
	if got := res.A.Steps[1].Response; got != "OK" {
		t.Fatalf("复位 0 第二步响应 = %q，期望 OK", got)
	}
	if got := res.B.Steps[1].Response; got != "LOCKED" {
		t.Fatalf("复位 1 第二步响应 = %q，期望 LOCKED", got)
	}
	if len(res.A.Observations) != 1 || res.A.Observations[0].Value != "WELCOME/1.0" {
		t.Fatalf("复位历史缺少初态观察: %+v", res.A.Observations)
	}
}

// TestUnknownResponseKeepsBranch 验证未知响应的分支被保留：
// 分歧出现在未知响应边之下，仍能被定位。
func TestUnknownResponseKeepsBranch(t *testing.T) {
	dev := &leakyDevice{}
	res, err := resetcheck.Check(dev, resetcheck.Config{
		Sequences: [][]string{{"exotic", "auth", "auth"}},
		Resets:    3,
	})
	if err != nil {
		t.Fatalf("Check 出错: %v", err)
	}
	if res == nil {
		t.Fatal("未知响应分支下的差异应被发现")
	}
	wantPath := []string{"exotic", "auth", "auth"}
	if strings.Join(res.Path, ",") != strings.Join(wantPath, ",") {
		t.Fatalf("最短探测序列 = %v，期望 %v", res.Path, wantPath)
	}
	if res.A.Steps[0].Known || res.A.Steps[0].Response != resetcheck.Unknown {
		t.Fatalf("第一步应保留为未知响应，得到 %+v", res.A.Steps[0])
	}
	if res.B.Steps[2].Response != "LOCKED" {
		t.Fatalf("复位 1 末步响应 = %q，期望 LOCKED", res.B.Steps[2].Response)
	}
}

// TestInitialObservationDivergence 验证初态观察集本身不同即返回空路径分歧。
func TestInitialObservationDivergence(t *testing.T) {
	dev := &leakyDevice{persistentAttempts: 1}
	res, err := resetcheck.Check(dev, resetcheck.Config{
		Observations: []string{"attempts"},
		Sequences:    [][]string{{"auth"}},
		Resets:       2,
	})
	if err != nil {
		t.Fatalf("Check 出错: %v", err)
	}
	if res == nil {
		t.Fatal("初态观察不同应被发现")
	}
	if len(res.Path) != 0 {
		t.Fatalf("初态观察分歧的路径应为空，得到 %v", res.Path)
	}
	if res.A.Observations[0].Value != "attempts=1" || res.B.Observations[0].Value != "attempts=2" {
		t.Fatalf("初态观察历史不符: %q 对 %q",
			res.A.Observations[0].Value, res.B.Observations[0].Value)
	}
}

// TestCleanResetIsConsistent 验证真正清除状态的设备通过检查。
func TestCleanResetIsConsistent(t *testing.T) {
	res, err := resetcheck.Check(&cleanDevice{}, resetcheck.Config{
		Observations: []string{"welcome", "session"},
		Sequences:    [][]string{{"auth", "auth"}, {"ping"}, {"exotic", "auth"}},
		Resets:       4,
	})
	if err != nil {
		t.Fatalf("Check 出错: %v", err)
	}
	if res != nil {
		t.Fatalf("干净复位的设备不应有差异: %v", res)
	}
}

// TestSequenceExceedsMaxDepth 验证超过深度六的探测序列被拒绝。
func TestSequenceExceedsMaxDepth(t *testing.T) {
	long := make([]string, resetcheck.MaxDepth+1)
	for i := range long {
		long[i] = "auth"
	}
	_, err := resetcheck.Check(&leakyDevice{}, resetcheck.Config{
		Sequences: [][]string{long},
		Resets:    2,
	})
	if err == nil {
		t.Fatal("超过最大深度的序列应被拒绝")
	}
}

// TestBranchingRequiresCheckpointer 验证设备不支持快照时分支探索报错。
func TestBranchingRequiresCheckpointer(t *testing.T) {
	_, err := resetcheck.Check(noCheckpointDevice{}, resetcheck.Config{
		Sequences: [][]string{{"auth"}, {"ping"}},
		Resets:    2,
	})
	if err == nil {
		t.Fatal("分支行为树应要求设备实现 Checkpointer")
	}
}

// noCheckpointDevice 不实现 Checkpointer。
type noCheckpointDevice struct{}

func (noCheckpointDevice) Reset() error                  { return nil }
func (noCheckpointDevice) Observe(string) (string, bool) { return "", false }
func (noCheckpointDevice) Probe(string) (string, bool)   { return "", false }
