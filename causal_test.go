package causal

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, text string) []Event {
	t.Helper()
	events, err := ParseLog(text)
	if err != nil {
		t.Fatalf("ParseLog: %v", err)
	}
	return events
}

// 验收场景：A 12:00:00 发请求，B 的物理时钟显示 11:59:59 收到（倒流 1s）。
// 算法必须强制校正 B 的时钟基线，使因果图物理自洽。
func TestAcceptanceNegativeLatency(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00.000+08:00 serverA SEND msg-1 serverB
2026-10-02T11:59:59.000+08:00 serverB RECV msg-1 serverA
2026-10-02T12:00:01.000+08:00 serverB SEND msg-2 serverA
2026-10-02T12:00:03.000+08:00 serverA RECV msg-2 serverB
`)
	minLat := time.Millisecond
	g, err := Reconstruct(events, minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	// 校正前必须检测到负延迟反例。
	neg := g.NegativeDelays()
	if len(neg) != 1 {
		t.Fatalf("want 1 negative-delay edge, got %d", len(neg))
	}

	// A 是基准（offset=0），B 的基线被强制拉回至少 1s。
	if got := g.Offsets["serverA"]; got != 0 {
		t.Errorf("serverA offset = %v, want 0", got)
	}
	if got := g.Offsets["serverB"]; got != -1001*time.Millisecond {
		t.Errorf("serverB offset = %v, want -1.001s", got)
	}

	// 校正后：recv 不再早于 send。
	send := g.Events[0]
	recv := g.Events[1]
	if !g.CorrectedTime(recv).After(g.CorrectedTime(send)) {
		t.Errorf("corrected recv %v not after send %v",
			g.CorrectedTime(recv), g.CorrectedTime(send))
	}
	wantRecv := time.Date(2026, 10, 2, 12, 0, 0, 1e6, time.FixedZone("CST", 8*3600))
	if !g.CorrectedTime(recv).Equal(wantRecv) {
		t.Errorf("corrected recv = %v, want %v", g.CorrectedTime(recv), wantRecv)
	}

	// 全图无因果违反。
	if v := g.Violations(minLat); len(v) != 0 {
		t.Errorf("want no violations, got %d", len(v))
	}
}

// 跨时区 + 多机链式漂移：C 用 UTC 记录且时钟慢 2s。
func TestCrossTimezoneChain(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00.000+08:00 serverA SEND msg-1 serverB
2026-10-02T11:59:59.100+08:00 serverB RECV msg-1 serverA
2026-10-02T11:59:59.200+08:00 serverB SEND msg-2 serverC
2026-10-02T03:59:58.300+00:00 serverC RECV msg-2 serverB
2026-10-02T03:59:58.400+00:00 serverC SEND msg-3 serverB
2026-10-02T11:59:59.500+08:00 serverB RECV msg-3 serverC
`)
	minLat := time.Millisecond
	g, err := Reconstruct(events, minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if got := g.Offsets["serverB"]; got != -901*time.Millisecond {
		t.Errorf("serverB offset = %v, want -901ms", got)
	}
	if got := g.Offsets["serverC"]; got != -1802*time.Millisecond {
		t.Errorf("serverC offset = %v, want -1802ms", got)
	}
	if v := g.Violations(minLat); len(v) != 0 {
		t.Errorf("want no violations, got %d", len(v))
	}

	// 拓扑序：msg-1 SEND 最早，msg-3 RECV 最晚。
	order := g.TopoOrder()
	first, last := g.Events[order[0]], g.Events[order[len(order)-1]]
	if first.MsgID != "msg-1" || first.Kind != Send {
		t.Errorf("first event = %+v, want SEND msg-1", first)
	}
	if last.MsgID != "msg-3" || last.Kind != Recv {
		t.Errorf("last event = %+v, want RECV msg-3", last)
	}
}

// 因果不自洽的日志（双向均大幅倒流）必须报负环错误。
func TestInconsistentLogs(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00+08:00 serverA SEND msg-1 serverB
2026-10-02T11:59:50+08:00 serverB RECV msg-1 serverA
2026-10-02T11:59:50+08:00 serverB SEND msg-2 serverA
2026-10-02T11:59:40+08:00 serverA RECV msg-2 serverB
`)
	_, err := Reconstruct(events, time.Millisecond)
	if err == nil {
		t.Fatal("want negative-cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "negative cycle") {
		t.Errorf("error = %v, want mention of negative cycle", err)
	}
}

func TestUnpairedMessage(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00+08:00 serverA SEND msg-1 serverB
`)
	if _, err := Reconstruct(events, time.Millisecond); err == nil {
		t.Fatal("want error for send without recv")
	}
}

func TestParseLogLine(t *testing.T) {
	ev, err := ParseLogLine("2026-10-02T04:00:00+00:00 serverC SEND m-9 serverA")
	if err != nil {
		t.Fatalf("ParseLogLine: %v", err)
	}
	if ev.Server != "serverC" || ev.Kind != Send || ev.MsgID != "m-9" || ev.Peer != "serverA" {
		t.Errorf("unexpected event: %+v", ev)
	}
	// 同一绝对时刻的不同时区表示必须解析为相等时间。
	a, _ := ParseLogLine("2026-10-02T12:00:00+08:00 s SEND m p")
	b, _ := ParseLogLine("2026-10-02T04:00:00+00:00 s SEND m p")
	if !a.Time.Equal(b.Time) {
		t.Errorf("timezone mismatch: %v vs %v", a.Time, b.Time)
	}

	for _, bad := range []string{
		"2026-10-02T12:00:00+08:00 onlythree SEND",
		"not-a-time s SEND m p",
		"2026-10-02T12:00:00+08:00 s PING m p",
	} {
		if _, err := ParseLogLine(bad); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

// 无漂移时偏移量应全为零。
func TestNoSkewNoCorrection(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00.000+08:00 serverA SEND msg-1 serverB
2026-10-02T12:00:00.050+08:00 serverB RECV msg-1 serverA
`)
	g, err := Reconstruct(events, time.Millisecond)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	for s, off := range g.Offsets {
		if off != 0 {
			t.Errorf("offset[%s] = %v, want 0", s, off)
		}
	}
	if len(g.NegativeDelays()) != 0 {
		t.Error("want no negative delays")
	}
}

func TestReportAndDOT(t *testing.T) {
	events := mustParse(t, `
2026-10-02T12:00:00.000+08:00 serverA SEND msg-1 serverB
2026-10-02T11:59:59.000+08:00 serverB RECV msg-1 serverA
`)
	g, err := Reconstruct(events, time.Millisecond)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	rep := g.Report(time.Millisecond)
	for _, want := range []string{"offset", "负延迟", "msg-1", "校验通过"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report missing %q:\n%s", want, rep)
		}
	}
	dot := g.DOT()
	if !strings.Contains(dot, "digraph") || !strings.Contains(dot, "n0 -> n1") {
		t.Errorf("bad DOT output:\n%s", dot)
	}
}
