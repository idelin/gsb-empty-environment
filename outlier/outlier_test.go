package outlier

import (
	"math"
	"testing"
)

func baseConfig() Config {
	return Config{
		Consecutive5xxThreshold:            3,
		ConsecutiveGatewayFailureThreshold: 3,
		Interval:                           1000,
		SuccessRateMinHosts:                3,
		SuccessRateReqVolume:               5,
		StdevFactor:                        1000,
		BaseEjectionTime:                   1000,
		MaxEjectionTime:                    10000,
		MaxEjectionPercent:                 50,
	}
}

func mustDetector(t *testing.T, hosts []string, cfg Config) *Detector {
	t.Helper()
	d, err := New(hosts, cfg)
	if err != nil {
		t.Fatalf("New 返回意外错误: %v", err)
	}
	return d
}

func req(timeMs int64, host string, status int, localFail bool) RequestResult {
	return RequestResult{TimeMs: timeMs, Host: host, StatusCode: status, LocalOriginFailure: localFail}
}

func assertEjected(t *testing.T, d *Detector, host string, want bool) {
	t.Helper()
	if got := d.IsEjected(host); got != want {
		t.Fatalf("主机 %s 摘除状态 = %v, 期望 %v", host, got, want)
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, 期望 %v", name, got, want)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := baseConfig()
	if _, err := New(nil, cfg); err == nil {
		t.Fatal("空主机池应报错")
	}
	bad := cfg
	bad.Interval = 0
	if _, err := New([]string{"h1"}, bad); err == nil {
		t.Fatal("Interval<=0 应报错")
	}
	bad = cfg
	bad.BaseEjectionTime = 0
	if _, err := New([]string{"h1"}, bad); err == nil {
		t.Fatal("BaseEjectionTime<=0 应报错")
	}
	bad = cfg
	bad.MaxEjectionPercent = 101
	if _, err := New([]string{"h1"}, bad); err == nil {
		t.Fatal("越界 MaxEjectionPercent 应报错")
	}
	if _, err := New([]string{"h1", "h1"}, cfg); err == nil {
		t.Fatal("重复主机应报错")
	}
	if _, err := New([]string{"h1"}, cfg); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}

func TestUnknownHostAndTimeMonotonic(t *testing.T) {
	d := mustDetector(t, []string{"h1"}, baseConfig())
	if _, err := d.ApplyResult(req(1, "ghost", 500, false)); err == nil {
		t.Fatal("未知主机应报错")
	}
	if _, err := d.Advance(10); err != nil {
		t.Fatalf("Advance 报错: %v", err)
	}
	if _, err := d.Advance(5); err == nil {
		t.Fatal("时间倒退应报错")
	}
	if _, err := d.ApplyResult(req(9, "h1", 200, false)); err == nil {
		t.Fatal("事件时间倒退应报错")
	}
}

// 验收：连续 5xx 触发摘除，maxEjectionPercent=50 在 3 台池上拦住第 3 台，
// 并逐事件核对精确解禁时刻与到期自动恢复。
func TestConsecutive5xxCapAndRecovery(t *testing.T) {
	cfg := baseConfig() // cap 50%，base 1000ms，3 台主机
	d := mustDetector(t, []string{"h1", "h2", "h3"}, cfg)

	// 每个结果单独投递，逐事件核对。
	for i := int64(0); i < 2; i++ {
		out, err := d.ApplyResult(req(10+i, "h1", 500, false))
		if err != nil || out.Ejected != nil {
			t.Fatalf("h1 第 %d 个 5xx 不应摘除", i+1)
		}
	}
	out, err := d.ApplyResult(req(12, "h1", 500, false))
	if err != nil || out.Ejected == nil {
		t.Fatalf("h1 第 3 个连续 5xx 应摘除: %+v err=%v", out, err)
	}
	if out.Ejected.EjectionCount != 1 || out.Ejected.DurationMs != 1000 ||
		out.Ejected.RecoverAtMs != 1012 || out.Ejected.Reason != ReasonConsecutive5xx {
		t.Fatalf("h1 摘除记录异常: %+v", out.Ejected)
	}
	assertEjected(t, d, "h1", true)

	// h2 成为第二台摘除（2/3 = 66.7% > 50%，但摘除前占比 1/3 < 50%，允许）。
	for i := int64(0); i < 3; i++ {
		if _, err := d.ApplyResult(req(20+i, "h2", 503, false)); err != nil {
			t.Fatal(err)
		}
	}
	hs, _ := d.Host("h2")
	if !hs.Ejected || hs.RecoverAtMs != 1022 {
		t.Fatalf("h2 应摘除且解禁时刻 1022, 实际 %+v", hs)
	}

	// h3 达到阈值但比例帽拦截：摘除前已有 2/3 摘除，占比不严格低于 50%。
	out = nil
	for i := int64(0); i < 3; i++ {
		var err error
		out, err = d.ApplyResult(req(30+i, "h3", 500, false))
		if err != nil {
			t.Fatal(err)
		}
	}
	if out.Ejected != nil || out.Blocked == nil || out.Blocked.Host != "h3" {
		t.Fatalf("h3 应被比例帽拦截, out=%+v", out)
	}
	assertEjected(t, d, "h3", false)
	if d.EjectedCount() != 2 {
		t.Fatalf("当前摘除数应为 2, 实际 %d", d.EjectedCount())
	}

	// 恰好在 h1 解禁时刻之前仍摘除，到达解禁时刻（含相等）自动恢复。
	recovered, err := d.Advance(1011)
	if err != nil || len(recovered) != 0 {
		t.Fatalf("1011 时刻不应有恢复: %+v", recovered)
	}
	assertEjected(t, d, "h1", true)
	recovered, err = d.Advance(1012)
	if err != nil || len(recovered) != 1 || recovered[0].Host != "h1" {
		t.Fatalf("1012 时刻 h1 应恢复: %+v err=%v", recovered, err)
	}
	assertEjected(t, d, "h1", false)

	// h1 恢复后比例帽腾出名额，新的连续错误可再次摘除。
	for i := int64(0); i < 3; i++ {
		if _, err := d.ApplyResult(req(1013+int64(i), "h1", 500, false)); err != nil {
			t.Fatal(err)
		}
	}
	hs, _ = d.Host("h1")
	if !hs.Ejected || hs.EjectionCount != 2 || hs.RecoverAtMs != 1015+2000 {
		t.Fatalf("h1 二次摘除倍数应为 2、时长 2000: %+v", hs)
	}
}

// 验收：cap=0 时任何摘除都不允许（首台特例仅在上限 >0 时生效）。
func TestZeroPercentBlocksEverything(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxEjectionPercent = 0
	d := mustDetector(t, []string{"h1", "h2"}, cfg)
	out, err := d.ApplyResult(req(1, "h1", 500, false))
	if err != nil {
		t.Fatal(err)
	}
	out, err = d.ApplyResult(req(2, "h1", 500, false))
	if err != nil {
		t.Fatal(err)
	}
	out, err = d.ApplyResult(req(3, "h1", 500, false))
	if err != nil {
		t.Fatal(err)
	}
	if out.Ejected != nil || out.Blocked == nil {
		t.Fatalf("cap=0 必须拦截首台摘除: %+v", out)
	}
	if d.EjectedCount() != 0 {
		t.Fatalf("cap=0 时摘除数必须为 0")
	}
}

// 验收：当前 0 台被摘除且比例上限 >0 时，即使 1/N 不严格低于 cap 也允许首台；
// 首台摘除后第二台立即被帽拦住。
func TestFirstHostExceptionUnderSmallCap(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxEjectionPercent = 10 // 5 台池：1/5=20% > 10%，整数比较下首台靠特例放行
	d := mustDetector(t, []string{"h1", "h2", "h3", "h4", "h5"}, cfg)

	for i := int64(0); i < 3; i++ {
		if _, err := d.ApplyResult(req(i, "h1", 500, false)); err != nil {
			t.Fatal(err)
		}
	}
	assertEjected(t, d, "h1", true)

	out := &Outcome{}
	for i := int64(0); i < 3; i++ {
		var err error
		out, err = d.ApplyResult(req(10+i, "h2", 500, false))
		if err != nil {
			t.Fatal(err)
		}
	}
	if out.Ejected != nil || out.Blocked == nil || out.Blocked.Host != "h2" {
		t.Fatalf("首台之后的摘除必须被比例帽拦截: %+v", out)
	}
	if d.EjectedCount() != 1 {
		t.Fatalf("摘除数应恒为 1, 实际 %d", d.EjectedCount())
	}
}

// 验收：连续本地源（网关）错误独立触发摘除，且两类计数器互不串用；
// 一次成功请求同时清零两条连续错误链。
func TestGatewayFailureAndSuccessReset(t *testing.T) {
	cfg := baseConfig()
	d := mustDetector(t, []string{"h1", "h2", "h3", "h4"}, cfg)

	// 阶段一：两条连续错误链独立累计，异类失败互相打断，成功请求统一清零。
	d.ApplyResult(req(1, "h1", 200, true))
	d.ApplyResult(req(2, "h1", 200, true))
	hs, _ := d.Host("h1")
	if hs.ConsecutiveGatewayError != 2 || hs.Consecutive5xx != 0 {
		t.Fatalf("两次网关错误后计数器异常: %+v", hs)
	}
	out, _ := d.ApplyResult(req(3, "h1", 500, false))
	if out.Ejected != nil {
		t.Fatalf("未达阈值不应摘除: %+v", out.Ejected)
	}
	hs, _ = d.Host("h1")
	if hs.ConsecutiveGatewayError != 0 || hs.Consecutive5xx != 1 {
		t.Fatalf("纯 5xx 应打断网关链并累计 5xx 链: %+v", hs)
	}

	// 一次成功请求清零两条链。
	d.ApplyResult(req(4, "h1", 200, false))
	hs, _ = d.Host("h1")
	if hs.Consecutive5xx != 0 || hs.ConsecutiveGatewayError != 0 {
		t.Fatalf("成功请求应清零连续错误: %+v", hs)
	}

	// 阶段二：连续 3 次本地源错误触发网关摘除。
	for i := int64(0); i < 3; i++ {
		out, _ = d.ApplyResult(req(10+i, "h1", 200, true))
	}
	if out.Ejected == nil || out.Ejected.Reason != ReasonConsecutiveGatewayFailure {
		t.Fatalf("应因连续网关错误摘除: %+v", out)
	}

	// 阶段三：502 同时是 5xx 与网关错误，5xx 触发器优先命中。
	for i := int64(0); i < 3; i++ {
		out, _ = d.ApplyResult(req(20+i, "h2", 502, true))
	}
	if out.Ejected == nil || out.Ejected.Reason != ReasonConsecutive5xx {
		t.Fatalf("502 应优先命中 5xx 触发器: %+v", out)
	}
}

// 验收：连续两次摘除时长翻倍，并在 maxEjectionTime 处触顶。
func TestEjectionBackoffDoublingAndCap(t *testing.T) {
	cfg := baseConfig()
	cfg.BaseEjectionTime = 1000
	cfg.MaxEjectionTime = 2500
	cfg.MaxEjectionPercent = 100
	d := mustDetector(t, []string{"h1"}, cfg)

	eject := func(at int64) *Ejection {
		t.Helper()
		var out *Outcome
		for i := int64(0); i < 3; i++ {
			var err error
			out, err = d.ApplyResult(req(at+i, "h1", 500, false))
			if err != nil {
				t.Fatal(err)
			}
		}
		if out.Ejected == nil {
			t.Fatalf("时刻 %d 应摘除", at)
		}
		return out.Ejected
	}

	first := eject(0) // 0..2，倍数 1，时长 1000，解禁 1002
	if first.EjectionCount != 1 || first.DurationMs != 1000 || first.RecoverAtMs != 1002 {
		t.Fatalf("首次摘除异常: %+v", first)
	}
	second := eject(1002) // 1002..1004，倍数 2，时长 2000，解禁 3004
	if second.EjectionCount != 2 || second.DurationMs != 2000 || second.RecoverAtMs != 3004 {
		t.Fatalf("二次摘除时长应翻倍: %+v", second)
	}
	third := eject(3004) // 倍数 3，名义 3000，触顶 2500，解禁 5506
	if third.EjectionCount != 3 || third.DurationMs != 2500 || third.RecoverAtMs != 5506 {
		t.Fatalf("三次摘除应触顶 maxEjectionTime: %+v", third)
	}

	// MaxEjectionTime<=0 表示不设上限。
	cfg.MaxEjectionTime = 0
	d2 := mustDetector(t, []string{"h1"}, cfg)
	var out *Outcome
	for i := int64(0); i < 3; i++ {
		out, _ = d2.ApplyResult(req(int64(i), "h1", 500, false))
	}
	if out.Ejected.DurationMs != 1000 {
		t.Fatalf("无上限时首次时长应为 1000: %+v", out.Ejected)
	}
}

// 验收：有效主机数（样本量达标的主机）不足 successRateMinHosts 时跳过统计。
func TestSuccessRateSkippedWhenMinHostsNotMet(t *testing.T) {
	cfg := baseConfig()
	cfg.SuccessRateMinHosts = 3
	cfg.SuccessRateReqVolume = 5
	cfg.Consecutive5xxThreshold = 0 // 隔离统计触发器，避免连续 5xx 先行摘除
	cfg.ConsecutiveGatewayFailureThreshold = 0
	d := mustDetector(t, []string{"h1", "h2", "h3", "h4"}, cfg)

	// 仅两台主机达到 volume=5。
	for i := int64(0); i < 5; i++ {
		d.ApplyResult(req(i, "h1", 500, false)) // 0% 成功率
		d.ApplyResult(req(i, "h2", 200, false)) // 100% 成功率
	}
	report, err := d.Tick(1000)
	if err != nil {
		t.Fatal(err)
	}
	if report.StatsEvaluated {
		t.Fatal("有效主机仅 2 台 < minHosts=3，不应执行统计")
	}
	if report.EligibleHosts != 2 || len(report.Ejected) != 0 || len(report.Blocked) != 0 {
		t.Fatalf("跳过时不应产生摘除: %+v", report)
	}
	assertEjected(t, d, "h1", false)

	// 窗口已滚动：上一周期数据不延续。
	hs, _ := d.Host("h1")
	if hs.RequestsInWindow != 0 || hs.SuccessesInWindow != 0 {
		t.Fatalf("Tick 后窗口应清零: %+v", hs)
	}
}

// 验收：成功率严格低于 mean - stdev*factor/1000 才摘除，等于阈值不摘除；
// 同时核对均值、总体标准差与阈值的精确数值。
func TestSuccessRateThresholdStrictnessAndFormula(t *testing.T) {
	cfg := baseConfig()
	cfg.SuccessRateMinHosts = 4
	cfg.SuccessRateReqVolume = 2
	cfg.StdevFactor = 0 // 阈值恰为均值，便于精确核对严格比较
	cfg.Consecutive5xxThreshold = 0
	cfg.ConsecutiveGatewayFailureThreshold = 0
	d := mustDetector(t, []string{"h1", "h2", "h3", "h4"}, cfg)

	// 各 4 个请求：成功率 25%、25%、75%、75%；均值 50，总体标准差精确为 25。
	rates := []struct {
		host      string
		successes int
	}{{"h1", 1}, {"h2", 1}, {"h3", 3}, {"h4", 3}}
	ts := int64(0)
	for _, entry := range rates {
		host, successes := entry.host, entry.successes
		for i := 0; i < 4; i++ {
			status := 500
			if i < successes {
				status = 200
			}
			d.ApplyResult(req(ts, host, status, false))
			ts++
		}
	}
	report, err := d.Tick(1000)
	if err != nil {
		t.Fatal(err)
	}
	if !report.StatsEvaluated || report.EligibleHosts != 4 {
		t.Fatalf("应执行统计且 4 台均有效: %+v", report)
	}
	assertFloat(t, "MeanSuccessRate", report.MeanSuccessRate, 50)
	assertFloat(t, "StdevSuccessRate", report.StdevSuccessRate, 25)
	assertFloat(t, "Threshold", report.Threshold, 50)
	if len(report.Ejected) != 2 ||
		report.Ejected[0].Host != "h1" || report.Ejected[1].Host != "h2" {
		t.Fatalf("严格低于阈值 50 的 h1/h2(25%%) 应摘除, 75%% 主机保留: %+v", report.Ejected)
	}
	assertEjected(t, d, "h1", true)
	assertEjected(t, d, "h2", true)
	assertEjected(t, d, "h3", false)
	assertEjected(t, d, "h4", false)

	// stdevFactor=1000：阈值 = 50 - 25 = 25；25% 不严格低于 25，无人被摘除。
	cfg2 := cfg
	cfg2.StdevFactor = 1000
	d2, err := New([]string{"h1", "h2", "h3", "h4"}, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	ts = 0
	for _, entry := range rates {
		host, successes := entry.host, entry.successes
		for i := 0; i < 4; i++ {
			status := 500
			if i < successes {
				status = 200
			}
			d2.ApplyResult(req(ts, host, status, false))
			ts++
		}
	}
	report2, err := d2.Tick(1000)
	if err != nil {
		t.Fatal(err)
	}
	assertFloat(t, "factor=1000 阈值", report2.Threshold, 25)
	if len(report2.Ejected) != 0 {
		t.Fatalf("25%% 不严格低于阈值 25，不应摘除: %+v", report2.Ejected)
	}
}

// 验收：成功率统计摘除同样受 maxEjectionPercent 保护，超额判定记入 Blocked，
// 且按主机池顺序逐个过帽，先放行后拦截。
func TestSuccessRateEjectionBlockedByCap(t *testing.T) {
	cfg := baseConfig()
	cfg.SuccessRateMinHosts = 4
	cfg.SuccessRateReqVolume = 2
	cfg.StdevFactor = 0
	cfg.Consecutive5xxThreshold = 0
	cfg.ConsecutiveGatewayFailureThreshold = 0
	cfg.MaxEjectionPercent = 25 // 4 台池至多摘除 1 台
	d := mustDetector(t, []string{"h1", "h2", "h3", "h4"}, cfg)

	rates := []struct {
		host      string
		successes int
	}{{"h1", 1}, {"h2", 1}, {"h3", 3}, {"h4", 3}} // 25% 25% 75% 75%，均值 50
	ts := int64(0)
	for _, entry := range rates {
		for i := 0; i < 4; i++ {
			status := 500
			if i < entry.successes {
				status = 200
			}
			d.ApplyResult(req(ts, entry.host, status, false))
			ts++
		}
	}
	report, err := d.Tick(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Ejected) != 1 || report.Ejected[0].Host != "h1" {
		t.Fatalf("比例帽应仅放行主机池顺序首台 h1: %+v", report.Ejected)
	}
	if len(report.Blocked) != 1 || report.Blocked[0].Host != "h2" ||
		report.Blocked[0].Reason != ReasonSuccessRate {
		t.Fatalf("h2 应被比例帽拦截: %+v", report.Blocked)
	}
	if d.EjectedCount() != 1 {
		t.Fatalf("实际摘除数必须为 1, 实际 %d", d.EjectedCount())
	}
}

// 验收：主机恢复后连续健康运行满一个完整统计周期则递减 ejectionCount，
// 每个完整周期至多衰减一次；期间再次摘除则重置衰减计时。
func TestDecayAfterHealthyIntervalAndReEjectionReset(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxEjectionPercent = 100
	cfg.BaseEjectionTime = 1000
	cfg.MaxEjectionTime = 10000
	d := mustDetector(t, []string{"h1"}, cfg)

	// 首次摘除：count=1，t=2 摘除，t=1002 解禁。
	for i := int64(0); i < 3; i++ {
		d.ApplyResult(req(i, "h1", 500, false))
	}
	hs, _ := d.Host("h1")
	if hs.EjectionCount != 1 || hs.RecoverAtMs != 1002 {
		t.Fatalf("首次摘除状态异常: %+v", hs)
	}

	// t=1002 到期恢复，健康计时起点记录为 1002。
	recovered, _ := d.Advance(1002)
	if len(recovered) != 1 {
		t.Fatalf("应在 1002 恢复: %+v", recovered)
	}
	hs, _ = d.Host("h1")
	if hs.Ejected || hs.EjectionCount != 1 || hs.HealthySinceMs != 1002 {
		t.Fatalf("恢复后应保留 count=1 并记录健康起点: %+v", hs)
	}

	// 不足一个完整周期的 Tick 不衰减。
	report, err := d.Tick(1500)
	if err != nil {
		t.Fatal(err)
	}
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 1 || hs.HealthySinceMs != 1002 {
		t.Fatalf("未满完整周期不应衰减: %+v", hs)
	}

	// 满一个完整周期（1002 -> 2002）：count 1->0，衰减结束。
	report, err = d.Tick(2002)
	if err != nil {
		t.Fatal(err)
	}
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 0 || hs.HealthySinceMs != -1 {
		t.Fatalf("健康满周期应衰减至 0 并清除计时: %+v report=%+v", hs, report)
	}

	// 再次摘除两次制造 count=2，验证多级衰减每周期 -1。
	for i := int64(0); i < 3; i++ {
		d.ApplyResult(req(2003+i, "h1", 500, false))
	}
	for i := int64(0); i < 3; i++ {
		d.ApplyResult(req(4005+i, "h1", 500, false)) // 首次已于 3005 解禁
	}
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 2 || !hs.Ejected {
		t.Fatalf("两次摘除后 count 应为 2: %+v", hs)
	}

	// 恢复后第一个完整周期：2->1 并重新计时；第二个完整周期：1->0。
	recoverAt := hs.RecoverAtMs
	d.Advance(recoverAt)
	d.Tick(recoverAt + cfg.Interval)
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 1 {
		t.Fatalf("首个健康周期后 count 应为 1: %+v", hs)
	}
	d.Tick(recoverAt + 2*cfg.Interval)
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 0 {
		t.Fatalf("第二个健康周期后 count 应为 0: %+v", hs)
	}

	// 再摘除后在健康期内重新出事：衰减计时被重置，不满周期不衰减。
	for i := int64(0); i < 3; i++ {
		d.ApplyResult(req(recoverAt+2*cfg.Interval+10+int64(i), "h1", 500, false))
	}
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 1 || !hs.Ejected || hs.HealthySinceMs != -1 {
		t.Fatalf("再摘除应 count=1 且重置健康计时: %+v", hs)
	}
	recAt2 := hs.RecoverAtMs
	d.Advance(recAt2)
	d.Tick(recAt2 + cfg.Interval - 1)
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 1 {
		t.Fatalf("恢复后未满周期不应衰减: %+v", hs)
	}
	d.Tick(recAt2 + cfg.Interval)
	hs, _ = d.Host("h1")
	if hs.EjectionCount != 0 {
		t.Fatalf("满周期后应衰减至 0: %+v", hs)
	}
}
