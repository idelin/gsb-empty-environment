package netsec

import (
	"errors"
	"strings"
	"testing"
)

// validCertificate 构造一份合法证书：
//
//	程序: 0: input x∈[0,10]  1: y=x+5  2: input z∈[3,12]  3: 分支合流点
//	步骤: 0: x∈[0,10]        (包含, 绑定指令0)
//	      1: y∈[5,15]        (加常数, 前提0, 绑定指令1)
//	      2: z∈[3,12]        (包含, 绑定指令2)
//	      3: w∈[5,12]        (交集, 前提1,2, 绑定指令2)
//	      4: u∈[3,15]        (合流, 前提1,2, 绑定指令3)
//	      5: u∈[0,20]        (弱化, 前提4, 绑定指令3)
func validCertificate() *Certificate {
	return &Certificate{
		Program: []Instruction{
			{Kind: InstrInput, Bounds: Interval{Lo: 0, Hi: 10}},
			{Kind: InstrAdd, Const: 5},
			{Kind: InstrInput, Bounds: Interval{Lo: 3, Hi: 12}},
			{Kind: InstrBranch},
		},
		Steps: []Step{
			{Rule: RuleContain, Instr: 0, Claim: Interval{Lo: 0, Hi: 10}},
			{Rule: RuleAddConst, Refs: []int{0}, Instr: 1, Claim: Interval{Lo: 5, Hi: 15}},
			{Rule: RuleContain, Instr: 2, Claim: Interval{Lo: 3, Hi: 12}},
			{Rule: RuleIntersect, Refs: []int{1, 2}, Instr: 2, Claim: Interval{Lo: 5, Hi: 12}},
			{Rule: RuleJoin, Refs: []int{1, 2}, Instr: 3, Claim: Interval{Lo: 3, Hi: 15}},
			{Rule: RuleContain, Refs: []int{4}, Instr: 3, Claim: Interval{Lo: 0, Hi: 20}},
		},
		Goal: 5,
	}
}

func TestVerifyAcceptsValidCertificate(t *testing.T) {
	if err := Verify(validCertificate()); err != nil {
		t.Fatalf("合法证书被拒绝: %v", err)
	}
}

// TestVerifyTamper 逐字段篡改证书，断言核验拒绝且错误定位到正确的步骤与字段。
func TestVerifyTamper(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(c *Certificate)
		wantStep  int
		wantField string
		wantIn    string // Reason 中应包含的子串，可为空
	}{
		{"结论下界非法强化", func(c *Certificate) { c.Steps[0].Claim.Lo = 1 }, 0, "claim", "非法强化"},
		{"结论上界非法强化", func(c *Certificate) { c.Steps[1].Claim.Hi = 14 }, 1, "claim", "非法强化"},
		{"交集结论缩窄", func(c *Certificate) { c.Steps[3].Claim = Interval{Lo: 6, Hi: 12} }, 3, "claim", "非法强化"},
		{"合流结论缩窄", func(c *Certificate) { c.Steps[4].Claim = Interval{Lo: 3, Hi: 14} }, 4, "claim", "非法强化"},
		{"弱化步骤反向强化", func(c *Certificate) { c.Steps[5].Claim = Interval{Lo: 4, Hi: 15} }, 5, "claim", "非法强化"},
		{"空结论冒充事实", func(c *Certificate) { c.Steps[1].Claim = Interval{Lo: 10, Hi: 9} }, 1, "claim", "非法强化"},

		{"前提自引用成环", func(c *Certificate) { c.Steps[1].Refs = []int{1} }, 1, "refs", "循环"},
		{"前提前向引用", func(c *Certificate) { c.Steps[1].Refs = []int{4} }, 1, "refs", "循环"},
		{"前提编号越界", func(c *Certificate) { c.Steps[1].Refs = []int{99} }, 1, "refs", "越界"},
		{"前提编号为负", func(c *Certificate) { c.Steps[1].Refs = []int{-1} }, 1, "refs", "越界"},
		{"前提数量不足", func(c *Certificate) { c.Steps[3].Refs = []int{1} }, 3, "refs", "需要 2 个前提"},
		{"前提数量过多", func(c *Certificate) { c.Steps[1].Refs = []int{0, 0} }, 1, "refs", "需要 1 个前提"},
		{"包含规则前提过多", func(c *Certificate) { c.Steps[5].Refs = []int{4, 3} }, 5, "refs", "至多 1 个前提"},

		{"绑定指令越界", func(c *Certificate) { c.Steps[1].Instr = 99 }, 1, "instr", "越界"},
		{"绑定指令为负", func(c *Certificate) { c.Steps[1].Instr = -1 }, 1, "instr", "越界"},
		{"加常数绑定输入指令", func(c *Certificate) { c.Steps[1].Instr = 0 }, 1, "instr", "加法指令"},
		{"合流绑定加法指令", func(c *Certificate) { c.Steps[4].Instr = 1 }, 4, "instr", "分支指令"},
		{"基础包含绑定分支指令", func(c *Certificate) { c.Steps[0].Instr = 3 }, 0, "instr", "输入指令"},

		{"规则编号非法", func(c *Certificate) { c.Steps[0].Rule = Rule(99) }, 0, "rule", "非法"},
		{"交集伪装成合流", func(c *Certificate) { c.Steps[3].Rule = RuleJoin; c.Steps[3].Instr = 3 }, 3, "claim", "非法强化"},
		{"合流伪装成加常数", func(c *Certificate) { c.Steps[4].Rule = RuleAddConst; c.Steps[4].Instr = 1 }, 4, "refs", "需要 1 个前提"},

		{"篡改加法常量", func(c *Certificate) { c.Program[1].Const = 6 }, 1, "claim", "非法强化"},
		{"篡改输入边界", func(c *Certificate) { c.Program[0].Bounds.Hi = 11 }, 0, "claim", "非法强化"},
		{"篡改指令类别", func(c *Certificate) { c.Program[1].Kind = InstrBranch }, 1, "instr", "加法指令"},
		{"程序为空", func(c *Certificate) { c.Program = nil }, -1, "program", "为空"},
		{"输入指令边界为空", func(c *Certificate) { c.Program[0].Bounds = Interval{Lo: 5, Hi: 4} }, -1, "program", "边界为空"},

		{"最终结论越界", func(c *Certificate) { c.Goal = 99 }, -1, "goal", "越界"},
		{"最终结论为负", func(c *Certificate) { c.Goal = -1 }, -1, "goal", "越界"},
		{"步骤为空", func(c *Certificate) { c.Steps = nil; c.Goal = 0 }, -1, "steps", "为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := validCertificate()
			tc.mutate(cert)
			err := Verify(cert)
			if err == nil {
				t.Fatalf("篡改后证书被错误接受")
			}
			var verr *Error
			if !errors.As(err, &verr) {
				t.Fatalf("错误类型应为 *Error，实际 %T: %v", err, err)
			}
			if verr.Step != tc.wantStep {
				t.Errorf("拒绝定位步骤 = %d，期望 %d (%v)", verr.Step, tc.wantStep, verr)
			}
			if verr.Field != tc.wantField {
				t.Errorf("拒绝定位字段 = %q，期望 %q (%v)", verr.Field, tc.wantField, verr)
			}
			if tc.wantIn != "" && !strings.Contains(verr.Reason, tc.wantIn) {
				t.Errorf("拒绝原因 %q 应包含 %q", verr.Reason, tc.wantIn)
			}
		})
	}
}

// TestVerifyAcceptsWeakeningAndEmptyIntersection 覆盖合法边界：
// 弱化链与空交集（bottom 可推出任意结论）。
func TestVerifyAcceptsWeakeningAndEmptyIntersection(t *testing.T) {
	cert := &Certificate{
		Program: []Instruction{
			{Kind: InstrInput, Bounds: Interval{Lo: 0, Hi: 5}},
			{Kind: InstrInput, Bounds: Interval{Lo: 10, Hi: 20}},
		},
		Steps: []Step{
			{Rule: RuleContain, Instr: 0, Claim: Interval{Lo: 0, Hi: 5}},
			{Rule: RuleContain, Instr: 1, Claim: Interval{Lo: 10, Hi: 20}},
			// 空交集：两区间不相交
			{Rule: RuleIntersect, Refs: []int{0, 1}, Instr: 0, Claim: Interval{Lo: 6, Hi: 5}},
			// 弱化到过近似区间
			{Rule: RuleContain, Refs: []int{0}, Instr: 0, Claim: Interval{Lo: -100, Hi: 100}},
		},
		Goal: 3,
	}
	if err := Verify(cert); err != nil {
		t.Fatalf("合法边界证书被拒绝: %v", err)
	}
}

// TestSemanticEscapeAbstains 加常数使取值逸出小域时，语义层弃权而规则层仍核验。
func TestSemanticEscapeAbstains(t *testing.T) {
	cert := &Certificate{
		Program: []Instruction{
			{Kind: InstrInput, Bounds: Interval{Lo: 0, Hi: 10}},
			{Kind: InstrAdd, Const: 1000},
		},
		Steps: []Step{
			{Rule: RuleContain, Instr: 0, Claim: Interval{Lo: 0, Hi: 10}},
			{Rule: RuleAddConst, Refs: []int{0}, Instr: 1, Claim: Interval{Lo: 1000, Hi: 1010}},
		},
		Goal: 1,
	}
	if err := Verify(cert); err != nil {
		t.Fatalf("逸出小域的合法证书被拒绝: %v", err)
	}
	// 同样逸出但结论被强化，规则层必须拒绝。
	cert.Steps[1].Claim = Interval{Lo: 1001, Hi: 1010}
	var verr *Error
	if err := Verify(cert); !errors.As(err, &verr) || verr.Step != 1 || verr.Field != "claim" {
		t.Fatalf("逸出小域的非法强化未被正确定位: %v", err)
	}
}

// TestOverflowSaturation 加常数溢出时饱和到过近似区间，不产生回绕。
func TestOverflowSaturation(t *testing.T) {
	iv := AddConst(Interval{Lo: maxInt64 - 3, Hi: maxInt64 - 1}, 10)
	if iv.Hi != maxInt64 || iv.Lo > iv.Hi {
		t.Fatalf("正向溢出未饱和: %+v", iv)
	}
	iv = AddConst(Interval{Lo: minInt64 + 1, Hi: minInt64 + 3}, -10)
	if iv.Lo != minInt64 || iv.Lo > iv.Hi {
		t.Fatalf("负向溢出未饱和: %+v", iv)
	}
}
