package netsec

import (
	"sync"
	"testing"
	"time"
)

const (
	sigSegv = 11
	sigAbrt = 6
)

func crashEvent(source string, length, signal int) ExitEvent {
	return ExitEvent{
		Source:     source,
		PayloadLen: length,
		Signal:     signal,
		ExitCode:   0,
	}
}

func cleanExitEvent(source string, length int) ExitEvent {
	return ExitEvent{
		Source:     source,
		PayloadLen: length,
		ExitCode:   0,
	}
}

// 场景一：同一来源逐字节增长、签名一致的崩溃序列在达到阈值时被封禁。
func TestBytewiseCanaryBruteForceIsBanned(t *testing.T) {
	var banned []string
	det := New(Config{
		Threshold: 8,
		MaxStep:   1,
		OnBan:     func(src string) { banned = append(banned, src) },
	})

	// 阈值前的每次崩溃都不应封禁。
	for length := 100; length < 107; length++ {
		if det.Observe(crashEvent("10.0.0.1", length, sigSegv)) {
			t.Fatalf("length=%d 不应在达到阈值前封禁", length)
		}
	}

	// 第 8 次崩溃（100..107）达到阈值，触发且仅触发一次回调。
	if !det.Observe(crashEvent("10.0.0.1", 107, sigSegv)) {
		t.Fatal("达到阈值的第 8 次崩溃应返回已封禁")
	}
	if len(banned) != 1 || banned[0] != "10.0.0.1" {
		t.Fatalf("封禁回调异常，got %v", banned)
	}
	if !det.Banned("10.0.0.1") {
		t.Fatal("Banned 应报告该来源已封禁")
	}

	// 封禁后的继续攻击不重复回调。
	if !det.Observe(crashEvent("10.0.0.1", 108, sigSegv)) {
		t.Fatal("已封禁来源的后续事件应继续返回封禁状态")
	}
	if len(banned) != 1 {
		t.Fatalf("重复事件不应再次触发回调，got %d 次", len(banned))
	}

	stats := det.Stats()
	if stats.BansIssued != 1 || stats.ActiveBans != 1 {
		t.Fatalf("统计异常: %+v", stats)
	}
}

// 场景二：多个来源各自只贡献少量崩溃时，任何来源都不应被封禁。
func TestSpreadCrashesAcrossSourcesDoNotBan(t *testing.T) {
	var bans int
	det := New(Config{
		Threshold: 8,
		MaxStep:   1,
		OnBan:     func(string) { bans++ },
	})

	// 8 个来源，每个只有 3 次看似爆破的崩溃，均低于阈值。
	for sourceID := 0; sourceID < 8; sourceID++ {
		src := "10.1.0." + string(rune('0'+sourceID))
		for length := 50; length < 53; length++ {
			if det.Observe(crashEvent(src, length, sigSegv)) {
				t.Fatalf("来源 %s 崩溃数低于阈值，不应封禁", src)
			}
		}
	}

	if bans != 0 {
		t.Fatalf("分散崩溃不应触发封禁，got %d", bans)
	}
	if got := det.Stats().CrashesObserved; got != 24 {
		t.Fatalf("应记录 24 次崩溃，got %d", got)
	}
}

// 场景三：偶发崩溃（签名变化、长度不增长、穿插正常退出）不会累积成封禁。
func TestOccasionalCrashesDoNotBan(t *testing.T) {
	var bans int
	det := New(Config{
		Threshold: 5,
		MaxStep:   1,
		OnBan:     func(string) { bans++ },
	})
	src := "10.2.0.9"

	// 远多于阈值的事件，但每次增长模式都被打断。
	for round := 0; round < 20; round++ {
		base := round * 10
		// 同长度重复：不满足严格递增，序列重置。
		det.Observe(crashEvent(src, base, sigSegv))
		det.Observe(crashEvent(src, base, sigSegv))
		// 签名变化：序列重置。
		det.Observe(crashEvent(src, base+1, sigAbrt))
		// 长度跳跃过大（超过 MaxStep）：序列重置。
		det.Observe(crashEvent(src, base+9, sigSegv))
		// 正常退出穿插：既不计数也不中断。
		det.Observe(cleanExitEvent(src, base+10))
	}

	if bans != 0 {
		t.Fatalf("偶发崩溃模式不应封禁，got %d", bans)
	}
	if det.Banned(src) {
		t.Fatal("正常客户端不应处于封禁状态")
	}
}

// 场景四：攻击期间穿插成功连接不掩盖爆破；同时验证完全正常的客户端永不被封。
func TestSuccessfulConnectionsInterleavedAndBenignClient(t *testing.T) {
	var bans int
	det := New(Config{
		Threshold: 6,
		MaxStep:   1,
		OnBan:     func(string) { bans++ },
	})
	attacker := "10.3.0.1"
	victim := "10.3.0.2"

	// 攻击者：崩溃序列之间多次成功处理连接，序列仍应累积。
	for length := 200; length < 206; length++ {
		det.Observe(cleanExitEvent(attacker, length))
		det.Observe(cleanExitEvent(attacker, length))
		if length == 205 {
			if !det.Observe(crashEvent(attacker, length, sigSegv)) {
				t.Fatal("穿插成功连接后攻击者仍应在阈值处被封禁")
			}
		} else {
			det.Observe(crashEvent(attacker, length, sigSegv))
		}
	}
	if bans != 1 {
		t.Fatalf("攻击者应被封禁一次，got %d", bans)
	}

	// 正常客户端：大量正常退出，偶发一次崩溃，永不到阈值。
	for length := 0; length < 100; length++ {
		det.Observe(cleanExitEvent(victim, length))
	}
	det.Observe(crashEvent(victim, 500, sigSegv))
	if det.Banned(victim) {
		t.Fatal("正常客户端不应被封禁")
	}
	if bans != 1 {
		t.Fatalf("正常客户端增加了封禁次数，got %d", bans)
	}
}

// 达到阈值前被一次正常增长模式之外的事件打断，需要重新计数。
func TestSequenceBreaksBeforeThreshold(t *testing.T) {
	var bans int
	det := New(Config{
		Threshold: 4,
		MaxStep:   1,
		OnBan:     func(string) { bans++ },
	})
	src := "10.4.0.1"

	det.Observe(crashEvent(src, 10, sigSegv))
	det.Observe(crashEvent(src, 11, sigSegv))
	det.Observe(crashEvent(src, 12, sigSegv))
	// 长度倒退，序列重置；此前的 3 次计数作废。
	det.Observe(crashEvent(src, 5, sigSegv))
	det.Observe(crashEvent(src, 6, sigSegv))
	det.Observe(crashEvent(src, 7, sigSegv))
	if bans != 0 {
		t.Fatal("被打断后不应封禁")
	}
	// 从倒退点重新连续 4 次才封禁。
	det.Observe(crashEvent(src, 8, sigSegv))
	if bans != 1 {
		t.Fatalf("重新连续达到阈值应封禁，got %d", bans)
	}
}

// MaxStep 允许调用方放宽长度增量（如每次猜测附带固定额外字节）。
func TestConfigurableMaxStep(t *testing.T) {
	var bans int
	det := New(Config{
		Threshold: 3,
		MaxStep:   4,
		OnBan:     func(string) { bans++ },
	})
	src := "10.5.0.1"

	det.Observe(crashEvent(src, 100, sigSegv))
	det.Observe(crashEvent(src, 103, sigSegv))
	det.Observe(crashEvent(src, 104, sigSegv))
	if bans != 1 {
		t.Fatalf("增量在 MaxStep 内应累积并封禁，got %d", bans)
	}

	// 新来源验证超步长仍会打断。
	other := "10.5.0.2"
	det.Observe(crashEvent(other, 100, sigSegv))
	det.Observe(crashEvent(other, 105, sigSegv)) // 增量 5 > 4
	det.Observe(crashEvent(other, 106, sigSegv))
	if det.Banned(other) {
		t.Fatal("超过 MaxStep 的跳跃应打断序列")
	}
}

// 带 TTL 的封禁到期后自动解除，再次攻击可重新封禁。
func TestBanTTLExpiryAndUnban(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	var bans int
	det := New(Config{
		Threshold: 2,
		MaxStep:   1,
		BanTTL:    time.Minute,
		Now:       func() time.Time { return now },
		OnBan:     func(string) { bans++ },
	})
	src := "10.6.0.1"

	det.Observe(crashEvent(src, 1, sigSegv))
	det.Observe(crashEvent(src, 2, sigSegv))
	if bans != 1 || !det.Banned(src) {
		t.Fatalf("应已封禁，bans=%d", bans)
	}

	now = now.Add(61 * time.Second)
	if det.Banned(src) {
		t.Fatal("TTL 到期后应自动解除封禁")
	}

	// Unban 对未封禁来源返回 false。
	if det.Unban(src) {
		t.Fatal("Unban 对未封禁来源应返回 false")
	}

	// 到期后再次攻击，重新计数并封禁。
	det.Observe(crashEvent(src, 10, sigSegv))
	det.Observe(crashEvent(src, 11, sigSegv))
	if bans != 2 {
		t.Fatalf("到期后再次命中应重新封禁，bans=%d", bans)
	}

	if !det.Unban(src) {
		t.Fatal("Unban 对已封禁来源应返回 true")
	}
	if det.Banned(src) {
		t.Fatal("Unban 后不应再报告封禁")
	}
}

// Reset 清空全部状态；并发调用不发生数据竞争。
func TestResetAndConcurrentObserve(t *testing.T) {
	det := New(Config{Threshold: 1000, MaxStep: 1})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			src := "10.7.0." + string(rune('0'+id))
			for length := 0; length < 200; length++ {
				det.Observe(crashEvent(src, length, sigSegv))
				_ = det.Banned(src)
			}
		}(worker)
	}
	wg.Wait()

	if got := det.Stats().CrashesObserved; got != 8*200 {
		t.Fatalf("并发事件计数异常: %d", got)
	}

	det.Reset()
	if got := det.Stats(); got != (Stats{}) {
		t.Fatalf("Reset 后统计应清零，got %+v", got)
	}
}

// 零值 Config 使用默认参数。
func TestDefaultConfig(t *testing.T) {
	det := New(Config{})
	if det.cfg.Threshold != DefaultThreshold || det.cfg.MaxStep != DefaultMaxStep {
		t.Fatalf("默认值异常: %+v", det.cfg)
	}

	src := "10.8.0.1"
	for length := 0; length < DefaultThreshold-1; length++ {
		if det.Observe(crashEvent(src, length, sigSegv)) {
			t.Fatal("默认阈值前不应封禁")
		}
	}
	if !det.Observe(crashEvent(src, DefaultThreshold-1, sigSegv)) {
		t.Fatal("达到默认阈值应封禁（无回调也应记录状态）")
	}
}

func TestExitEventIsCrash(t *testing.T) {
	if (ExitEvent{}).IsCrash() {
		t.Fatal("零值事件应视为正常退出")
	}
	if !(ExitEvent{Signal: sigSegv}).IsCrash() {
		t.Fatal("信号杀死应视为崩溃")
	}
	if !(ExitEvent{ExitCode: 1}).IsCrash() {
		t.Fatal("非零退出码应视为崩溃")
	}
}
