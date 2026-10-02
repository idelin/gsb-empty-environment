package pricing

import "testing"

// newSpecLedger 构造验收边界中的账本：
// 价格 10 在业务 [0,20) 有效、知悉于 2；价格 15 在业务 [5,10) 有效、知悉于 8。
func newSpecLedger() *Ledger {
	var l Ledger
	l.AddRule(0, 20, 10, 2)
	l.AddRule(5, 10, 15, 8)
	return &l
}

func TestSpecAcceptanceBoundaries(t *testing.T) {
	l := newSpecLedger()
	cases := []struct {
		name    string
		validAt int64
		txAt    int64
		want    int64
	}{
		{"业务7知悉7只见旧价", 7, 7, 10},
		{"业务7知悉8后入为主", 7, 8, 15},
		{"业务10新价区间外取旧价", 10, 8, 10},
		{"业务10知悉很大仍取10", 10, 1000, 10},
		{"业务0左闭边界", 0, 8, 10},
		{"业务19区间内", 19, 8, 10},
		{"业务5新价左边界且知悉8", 5, 8, 15},
		{"业务9新价区间内", 9, 8, 15},
		{"业务5知悉7新价未知悉", 5, 7, 10},
	}
	for _, c := range cases {
		got, err := l.PriceAt(c.validAt, c.txAt)
		if err != nil {
			t.Fatalf("%s: PriceAt(%d,%d) 返回错误: %v", c.name, c.validAt, c.txAt, err)
		}
		if got != c.want {
			t.Errorf("%s: PriceAt(%d,%d) = %d, 期望 %d", c.name, c.validAt, c.txAt, got, c.want)
		}
	}
}

func TestRightOpenValidInterval(t *testing.T) {
	l := newSpecLedger()
	if _, err := l.PriceAt(20, 100); err != ErrNotFound {
		t.Errorf("业务20为右开边界，期望 ErrNotFound，得到 %v", err)
	}
	if _, err := l.PriceAt(-1, 100); err != ErrNotFound {
		t.Errorf("业务-1在区间外，期望 ErrNotFound，得到 %v", err)
	}
}

func TestUnknownBeforeKnowledge(t *testing.T) {
	l := newSpecLedger()
	if _, err := l.PriceAt(7, 1); err != ErrNotFound {
		t.Errorf("知悉1早于所有规则知悉时间，期望 ErrNotFound，得到 %v", err)
	}
}

func TestRetractOnlyAffectsLaterKnowledge(t *testing.T) {
	l := newSpecLedger()
	l.Retract(9) // 知悉时间 9 撤销全部规则

	if _, err := l.PriceAt(7, 9); err != ErrNotFound {
		t.Errorf("撤销后知悉9查询应无价格，期望 ErrNotFound，得到 %v", err)
	}
	if got := l.MustPriceAt(7, 8); got != 15 {
		t.Errorf("撤销不影响知悉8的历史查询，期望 15，得到 %d", got)
	}
	if got := l.MustPriceAt(7, 7); got != 10 {
		t.Errorf("撤销不影响知悉7的历史查询，期望 10，得到 %d", got)
	}

	// 撤销后仍可登记新规则，且只对之后的知悉时间可见。
	l.AddRule(0, 20, 12, 10)
	if got := l.MustPriceAt(7, 10); got != 12 {
		t.Errorf("撤销后新规则在知悉10应生效，期望 12，得到 %d", got)
	}
	if _, err := l.PriceAt(7, 9); err != ErrNotFound {
		t.Errorf("新规则对知悉9不可见，期望 ErrNotFound，得到 %v", err)
	}
}

func TestSameKnowledgeTimeLaterInsertWins(t *testing.T) {
	var l Ledger
	l.AddRule(0, 10, 100, 5)
	l.AddRule(0, 10, 200, 5) // 同一知悉时间，后登记者胜出
	if got := l.MustPriceAt(3, 5); got != 200 {
		t.Errorf("知悉时间相同时应后入为主，期望 200，得到 %d", got)
	}
}
