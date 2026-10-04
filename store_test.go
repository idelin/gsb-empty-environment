package bitemporal

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// 时间线：
//   t1=2024-06-01 系统获知：salary 全年 2024 = 100
//   t2=2024-09-01 更正：2~3 月改为 120（拆分业务区间）
//   t3=2024-11-01 撤销该更正
var (
	t1 = d("2024-06-01")
	t2 = d("2024-09-01")
	t3 = d("2024-11-01")
)

func setup(t *testing.T) (*Store, int) {
	t.Helper()
	s := NewStore()
	if _, err := s.Put("salary", "100", d("2024-01-01"), d("2025-01-01"), t1); err != nil {
		t.Fatalf("初始写入失败: %v", err)
	}
	opID, err := s.Put("salary", "120", d("2024-02-01"), d("2024-04-01"), t2)
	if err != nil {
		t.Fatalf("更正失败: %v", err)
	}
	return s, opID
}

func TestCorrectionSplitsValidInterval(t *testing.T) {
	s, _ := setup(t)
	cur := s.Current("salary", t2)
	if len(cur) != 3 {
		t.Fatalf("更正后应有 3 个当前版本，实际 %d: %v", len(cur), cur)
	}
	want := []struct{ from, to, val string }{
		{"2024-01-01", "2024-02-01", "100"},
		{"2024-02-01", "2024-04-01", "120"},
		{"2024-04-01", "2025-01-01", "100"},
	}
	for i, w := range want {
		if !cur[i].ValidFrom.Equal(d(w.from)) || !cur[i].ValidTo.Equal(d(w.to)) || cur[i].Value != w.val {
			t.Errorf("版本 %d 不符合预期: got %s, want valid=[%s,%s) value=%s",
				i, cur[i], w.from, w.to, w.val)
		}
	}
	if err := s.CheckContiguous("salary", t2); err != nil {
		t.Errorf("边界相接校验失败: %v", err)
	}
	if err := s.ValidateNoOverlap("salary", t2); err != nil {
		t.Errorf("重叠校验失败: %v", err)
	}
}

func TestBitemporalQuery(t *testing.T) {
	s, _ := setup(t)
	cases := []struct {
		name           string
		validAt, txAt  time.Time
		want           string
	}{
		{"旧系统切面看2月(更正前)", d("2024-02-15"), t1, "100"},
		{"新系统切面看2月(更正后)", d("2024-02-15"), t2, "120"},
		{"新系统切面看5月(未受影响)", d("2024-05-15"), t2, "100"},
		{"旧系统切面看5月", d("2024-05-15"), t1, "100"},
		{"边界点: 4月1日起回到100", d("2024-04-01"), t2, "100"},
		{"边界点: 2月1日起为120", d("2024-02-01"), t2, "120"},
	}
	for _, c := range cases {
		got, ok := s.AsOf("salary", c.validAt, c.txAt)
		if !ok || got != c.want {
			t.Errorf("%s: AsOf=%q,%v want %q", c.name, got, ok, c.want)
		}
	}
	// 业务区间之外
	if _, ok := s.AsOf("salary", d("2025-06-01"), t2); ok {
		t.Error("业务区间外不应查到值")
	}
}

func TestUndoCorrection(t *testing.T) {
	s, opID := setup(t)
	if err := s.Undo(opID, t3); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	// 撤销后当前版本恢复为单一版本
	cur := s.Current("salary", t3)
	if len(cur) != 1 || cur[0].Value != "100" ||
		!cur[0].ValidFrom.Equal(d("2024-01-01")) || !cur[0].ValidTo.Equal(d("2025-01-01")) {
		t.Fatalf("撤销后应恢复为原始单版本, got %v", cur)
	}
	// 撤销切面下 2 月恢复为 100
	if got, _ := s.AsOf("salary", d("2024-02-15"), t3); got != "100" {
		t.Errorf("撤销后 2 月应为 100, got %q", got)
	}
	// 中间切面（t2）仍能看到更正结果 —— 历史不可篡改
	if got, _ := s.AsOf("salary", d("2024-02-15"), t2); got != "120" {
		t.Errorf("t2 切面应仍为 120, got %q", got)
	}
	if err := s.ValidateNoOverlap("salary", t3); err != nil {
		t.Errorf("撤销后重叠校验失败: %v", err)
	}
	// 重复撤销应报错
	if err := s.Undo(opID, t3); err == nil {
		t.Error("重复撤销应报错")
	}
}

func TestHistoryKeepsClosedVersions(t *testing.T) {
	s, _ := setup(t)
	h := s.History("salary")
	// 1 个被关闭的原始版本 + 3 个更正产生的版本 = 4
	if len(h) != 4 {
		t.Fatalf("历史应含 4 个版本, got %d", len(h))
	}
	closed := 0
	for _, v := range h {
		if !v.TxTo.Equal(Inf) {
			closed++
			if !v.TxTo.Equal(t2) {
				t.Errorf("旧版本应在 t2 被关闭, got TxTo=%s", fmtT(v.TxTo))
			}
		}
	}
	if closed != 1 {
		t.Errorf("应有 1 个被关闭版本, got %d", closed)
	}
}

func TestInvalidInterval(t *testing.T) {
	s := NewStore()
	if _, err := s.Put("k", "v", d("2024-05-01"), d("2024-01-01"), t1); err == nil {
		t.Error("非法区间应报错")
	}
}
