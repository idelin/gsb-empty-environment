package merge

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(days int) time.Time { return base.AddDate(0, 0, days) }

func findField(t *testing.T, r Result, field string) FieldResult {
	t.Helper()
	for _, f := range r.Fields {
		if f.Field == field {
			return f
		}
	}
	t.Fatalf("字段 %s 不在结果中", field)
	return FieldResult{}
}

func TestPriorityWins(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "name", Source: "crm", Priority: 1, Seq: 9, Value: ValueOf("低优先")},
		{Field: "name", Source: "erp", Priority: 3, Seq: 1, Value: ValueOf("高优先")},
	}, at(0))
	fr := findField(t, r, "name")
	if fr.Conflict {
		t.Fatal("不应报冲突")
	}
	if fr.Value.Kind != Val || fr.Value.V != "高优先" {
		t.Fatalf("期望高优先级来源胜出，得到 %+v", fr.Value)
	}
}

func TestSeqBreaksTie(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "name", Source: "a", Priority: 2, Seq: 1, Value: ValueOf("旧")},
		{Field: "name", Source: "b", Priority: 2, Seq: 5, Value: ValueOf("新")},
	}, at(0))
	fr := findField(t, r, "name")
	if fr.Value.V != "新" || fr.Conflict {
		t.Fatalf("同优先级应由入库序号大者胜出，得到 %+v conflict=%v", fr.Value, fr.Conflict)
	}
}

func TestConflictOnSamePrioritySameSeq(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "name", Source: "b", Priority: 2, Seq: 5, Value: ValueOf("乙")},
		{Field: "name", Source: "a", Priority: 2, Seq: 5, Value: ValueOf("甲")},
	}, at(0))
	fr := findField(t, r, "name")
	if !fr.Conflict {
		t.Fatal("同优先同序异值应报冲突")
	}
	if fr.Value.V != "甲" {
		t.Fatalf("冲突代表应按来源名确定性选取，得到 %+v", fr.Value)
	}
	var cw, cl int
	for _, e := range fr.Candidates {
		switch e.Disposition {
		case ConflictWinner:
			cw++
		case ConflictLoser:
			cl++
		}
	}
	if cw != 1 || cl != 1 {
		t.Fatalf("冲突处置计数异常: winner=%d loser=%d", cw, cl)
	}
}

func TestSameValueNoConflict(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "name", Source: "a", Priority: 2, Seq: 5, Value: ValueOf("同")},
		{Field: "name", Source: "b", Priority: 2, Seq: 5, Value: ValueOf("同")},
	}, at(0))
	fr := findField(t, r, "name")
	if fr.Conflict || fr.Value.V != "同" {
		t.Fatalf("同值并列不应报冲突，得到 %+v conflict=%v", fr.Value, fr.Conflict)
	}
}

func TestValidityWindow(t *testing.T) {
	cands := []Candidate{
		{Field: "status", Source: "old", Priority: 9, Seq: 9, From: at(-10), To: at(-1), Value: ValueOf("过期")},
		{Field: "status", Source: "cur", Priority: 1, Seq: 1, From: at(-1), To: at(10), Value: ValueOf("当前")},
		{Field: "status", Source: "fut", Priority: 9, Seq: 9, From: at(10), Value: ValueOf("未来")},
	}
	r := Adjudicate(cands, at(0))
	fr := findField(t, r, "status")
	if fr.Value.V != "当前" {
		t.Fatalf("窗口外候选应被过滤，得到 %+v", fr.Value)
	}
	for _, e := range fr.Candidates {
		if e.Candidate.Source != "cur" && e.Disposition != OutOfWindow {
			t.Fatalf("窗口外候选处置异常: %+v", e)
		}
	}
	// 边界：From 含、To 不含
	r = Adjudicate(cands, at(10))
	if findField(t, r, "status").Value.V != "未来" {
		t.Fatal("上界应为半开区间，t==To 时候选失效")
	}
	r = Adjudicate(cands, at(-1))
	if findField(t, r, "status").Value.V != "当前" {
		t.Fatal("下界应含，t==From 时候选生效")
	}
}

func TestMissingDoesNotOverrideNull(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "email", Source: "auth", Priority: 9, Seq: 9, Value: MissingValue()},
		{Field: "email", Source: "crm", Priority: 1, Seq: 1, Value: NullValue()},
	}, at(0))
	fr := findField(t, r, "email")
	if fr.Value.Kind != Null {
		t.Fatalf("缺失不应覆盖显式 null，得到 %+v", fr.Value)
	}
	if fr.Candidates[0].Disposition != IgnoredMissing {
		t.Fatalf("缺失候选应标记为不参与裁决，得到 %v", fr.Candidates[0].Disposition)
	}
}

func TestNullBeatsValueByPriority(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "email", Source: "auth", Priority: 9, Seq: 1, Value: NullValue()},
		{Field: "email", Source: "crm", Priority: 1, Seq: 1, Value: ValueOf("a@b.c")},
	}, at(0))
	if fr := findField(t, r, "email"); fr.Value.Kind != Null {
		t.Fatalf("显式 null 是真实候选，应按优先级胜出，得到 %+v", fr.Value)
	}
}

func TestAllMissingYieldsMissing(t *testing.T) {
	r := Adjudicate([]Candidate{
		{Field: "email", Source: "a", Priority: 9, Seq: 9, Value: MissingValue()},
	}, at(0))
	if fr := findField(t, r, "email"); fr.Value.Kind != Missing || fr.Conflict {
		t.Fatalf("全部缺失时结果应为 Missing，得到 %+v", fr.Value)
	}
}

func TestExplanationCoversEveryCandidate(t *testing.T) {
	in := []Candidate{
		{Field: "b", Source: "s1", Priority: 1, Seq: 1, Value: ValueOf(1)},
		{Field: "a", Source: "s2", Priority: 2, Seq: 2, To: at(-1), Value: ValueOf(2)},
		{Field: "a", Source: "s3", Priority: 2, Seq: 3, Value: MissingValue()},
		{Field: "a", Source: "s4", Priority: 2, Seq: 4, Value: ValueOf(4)},
	}
	r := Adjudicate(in, at(0))
	seen := 0
	for _, fr := range r.Fields {
		seen += len(fr.Candidates)
		for _, e := range fr.Candidates {
			if e.Note == "" {
				t.Fatalf("候选缺少解释: %+v", e.Candidate)
			}
		}
	}
	if seen != len(in) {
		t.Fatalf("解释未覆盖全部候选: %d/%d", seen, len(in))
	}
	// 字段输出按名排序
	if r.Fields[0].Field != "a" || r.Fields[1].Field != "b" {
		t.Fatalf("字段应按名排序: %+v", r.Fields)
	}
}
