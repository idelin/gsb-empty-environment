package recovery

import "testing"

func TestScenarios(t *testing.T) {
	cases := []struct {
		model    *Model
		feasible bool
		bound    int
	}{
		{LostAckModel(), true, 1},
		{LineSwitchModel(), true, 2},
		{StaleReceiptModel(), true, 2},
		{EarlyFinishModel(), true, 1},
		{NoCommonStepModel(), false, 0},
		{HostModel(), true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.model.Name, func(t *testing.T) {
			res, err := Solve(tc.model)
			if err != nil {
				t.Fatalf("Solve: %v", err)
			}
			if res.Feasible != tc.feasible {
				t.Fatalf("Feasible = %v, 期望 %v", res.Feasible, tc.feasible)
			}
			if tc.feasible && res.Bound != tc.bound {
				t.Fatalf("Bound = %d, 期望 %d", res.Bound, tc.bound)
			}
			if err := Verify(tc.model, res.Cert); err != nil {
				t.Fatalf("证书验证失败: %v", err)
			}
		})
	}
}

func TestHostStrategyShape(t *testing.T) {
	res, err := Solve(HostModel())
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Feasible {
		t.Fatal("组合宿主模型应可行")
	}
	// 唯一共同首步必须是探测:取回对旧密文无效,换代对已上线无效。
	first, ok := res.Cert.ActionFor(1, 2, 3, 4)
	if !ok {
		t.Fatal("初始信念缺少选定动作")
	}
	if first != ActProbe {
		t.Fatalf("共同首步 = %d, 期望 ActProbe(%d)", first, ActProbe)
	}
	if a, ok := res.Cert.ActionFor(1, 2); !ok || a != ActRetrieve {
		t.Fatalf("匹配分支动作 = %d, %v;期望 ActRetrieve", a, ok)
	}
	if a, ok := res.Cert.ActionFor(3); !ok || a != ActRotate {
		t.Fatalf("旧密文分支动作 = %d, %v;期望 ActRotate", a, ok)
	}
}

func TestVerifyRejectsTamperedBound(t *testing.T) {
	m := HostModel()
	res, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	bad := *res.Cert
	bad.Bound++
	if err := Verify(m, &bad); err == nil {
		t.Fatal("篡改上界后证书不应通过验证")
	}
}

func TestVerifyRejectsTamperedChoice(t *testing.T) {
	m := HostModel()
	res, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	bad := *res.Cert
	bad.Choice = map[string]Action{}
	for k, v := range res.Cert.Choice {
		bad.Choice[k] = v
	}
	// 旧密文分支上换线代价更高,不满足最优性。
	bad.Choice[BeliefKey(3)] = ActSwitchLine
	if err := Verify(m, &bad); err == nil {
		t.Fatal("篡改动作后证书不应通过验证")
	}
}

func TestVerifyRejectsBrokenTrap(t *testing.T) {
	m := NoCommonStepModel()
	res, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Feasible {
		t.Fatal("无共同首步模型应不可行")
	}
	if err := Verify(m, res.Cert); err != nil {
		t.Fatalf("合法陷阱证书应通过: %v", err)
	}
	bad := &Certificate{Feasible: false, Trap: []string{BeliefKey(3)}}
	if err := Verify(m, bad); err == nil {
		t.Fatal("不含初始信念的陷阱不应通过验证")
	}
}

func TestStrategyIgnoresSecretState(t *testing.T) {
	// 秘密任务已完成(状态 1)与仍有待投帧(状态 2)公开观察相同,
	// 证书只能给共同信念一个动作。
	res, err := Solve(EarlyFinishModel())
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if _, ok := res.Cert.ActionFor(1); ok {
		t.Fatal("策略不得单独依赖秘密完成状态")
	}
	if a, ok := res.Cert.ActionFor(1, 2); !ok || a != ActRetrieve {
		t.Fatalf("共同信念动作 = %d, %v;期望 ActRetrieve", a, ok)
	}
}
