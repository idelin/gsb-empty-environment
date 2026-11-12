package netsec

import "testing"

func principalsOf(t *testing.T, p Proof) []string {
	t.Helper()
	got := p.Principals()
	out := make([]string, 0, len(got))
	for _, principal := range got {
		out = append(out, string(principal))
	}
	return out
}

func TestProofUnionDeduplicatesPrincipals(t *testing.T) {
	a := Of("alice", "bob")
	b := Of("bob", "carol")
	merged := Union(a, b, Of("alice"))

	if got := merged.Count(); got != 3 {
		t.Fatalf("并集不同主体数 = %d，期望 3（bob/alice 重复出现只计一次）", got)
	}
	want := []string{"alice", "bob", "carol"}
	if got := principalsOf(t, merged); len(got) != len(want) {
		t.Fatalf("主体集合 = %v，期望 %v", got, want)
	}
	for i, name := range want {
		if got := principalsOf(t, merged); got[i] != name {
			t.Fatalf("主体集合 = %v，期望 %v", got, want)
		}
	}
}

func TestPolicyRejectsNonPositiveK(t *testing.T) {
	if _, err := NewPolicy(0); err == nil {
		t.Fatal("K=0 必须被拒绝")
	}
	if _, err := NewPolicy(-2); err == nil {
		t.Fatal("负 K 必须被拒绝")
	}
}

// 反例 1：复制不增加人数。
// 把仅由 2 个主体支持的值复制成成百上千行，不同主体数仍是 2，
// 在 K=3 的策略下永远不能去标——行数不能代替人数。
func TestCopyDoesNotInflatePrincipalCount(t *testing.T) {
	policy := mustPolicy(t, 3)
	base := NewTable([]string{"amount"},
		Row{"amount": NewValue(10, "alice", "bob")},
	)

	replicated := CopyRows(base, 100)
	if len(replicated.Rows) != 100 {
		t.Fatalf("复制后行数 = %d，期望 100", len(replicated.Rows))
	}

	agg := Aggregate(replicated, "amount", "total", SumInts)
	total := agg.Rows[0]["total"]
	if total.Proof.Count() != 2 {
		t.Fatalf("复制后聚合支持主体数 = %d，期望 2", total.Proof.Count())
	}
	if policy.CanRelease(total) {
		t.Fatal("100 行复制只来自 2 个主体，不能在 K=3 下去标")
	}

	if got := NewValue(1, "alice").Copy().Proof.Count(); got != 1 {
		t.Fatalf("单值复制后主体数 = %d，期望 1", got)
	}
}

// 反例 2：共享来源 + 一对多连接，连接贡献必须去重。
// 左表一行命中右表多行（扇出），且同一主体在连接两侧都出现；
// 输出行数被放大，但每个输出值的不同主体数不得按命中次数累加。
func TestJoinContributionsAreDeduplicated(t *testing.T) {
	policy := mustPolicy(t, 3)
	left := NewTable([]string{"oid", "left_v"},
		Row{"oid": NewValue("x", "alice"), "left_v": NewValue(1, "alice")},
	)
	// 右表两行同键：一行来自 alice（与左侧共享来源），一行来自 bob。
	right := NewTable([]string{"oid", "right_v"},
		Row{"oid": NewValue("x", "alice"), "right_v": NewValue(2, "alice")},
		Row{"oid": NewValue("x", "bob"), "right_v": NewValue(3, "bob")},
	)

	joined := Join(left, right, "oid", "oid")
	if len(joined.Rows) != 2 {
		t.Fatalf("内连接扇出行数 = %d，期望 2", len(joined.Rows))
	}
	// 第 0 行两侧都是 alice：共享来源去重后只有 1 个主体。
	if got := joined.Rows[0]["r.right_v"].Proof.Count(); got != 1 {
		t.Fatalf("共享来源行主体数 = %d，期望 1（alice 跨两侧只计一次）", got)
	}
	// 第 1 行右值来自 bob，并上左侧 alice，共 2 个不同主体。
	if got := joined.Rows[1]["r.right_v"].Proof.Count(); got != 2 {
		t.Fatalf("bob 命中行主体数 = %d，期望 2", got)
	}
	for i, row := range joined.Rows {
		if policy.CanRelease(row["r.right_v"]) {
			t.Fatalf("第 %d 行不足 3 个不同主体，K=3 下不能去标", i)
		}
	}

	// 再加入 carol 的第三条命中：去重后才真正达到 3 个不同主体。
	right.Rows = append(right.Rows, Row{
		"oid":     NewValue("x", "carol"),
		"right_v": NewValue(4, "carol"),
	})
	joined = Join(left, right, "oid", "oid")
	if len(joined.Rows) != 3 {
		t.Fatalf("扇出行数 = %d，期望 3", len(joined.Rows))
	}
	// 单个命中行仍只含左侧 alice 与该行右侧主体（如 alice+carol=2），
	// 扇出不会把别的命中行的主体算进来。
	if got := joined.Rows[2]["r.right_v"].Proof.Count(); got != 2 {
		t.Fatalf("第三命中行主体数 = %d，期望 2（alice、carol）", got)
	}
	// 对连接后的三行做聚合：不同主体并集为 alice、bob、carol，达到 K=3。
	joinedAgg := Aggregate(joined, "r.right_v", "joined_sum", SumInts).Rows[0]["joined_sum"]
	if got := joinedAgg.Proof.Count(); got != 3 {
		t.Fatalf("连接后聚合主体数 = %d，期望 3", got)
	}
	if !policy.CanRelease(joinedAgg) {
		t.Fatal("连接扇出三行经去重聚合得到 3 个不同主体，应当可以去标")
	}
}

// 反例 3：同一主体多行参与聚合，只贡献一个不同主体。
// alice 有 5 行、bob 有 5 行，共 10 行却只有 2 个主体；
// 聚合结果在 K=3 下仍敏感，不能因为行数达标而去标。
func TestAggregateCountsDistinctPrincipalsNotRows(t *testing.T) {
	policy := mustPolicy(t, 3)
	rows := make([]Row, 0, 10)
	for i := 0; i < 5; i++ {
		rows = append(rows, Row{"amount": NewValue(10, "alice")})
		rows = append(rows, Row{"amount": NewValue(10, "bob")})
	}
	raw := NewTable([]string{"amount"}, rows...)

	agg := Aggregate(raw, "amount", "total", SumInts)
	total := agg.Rows[0]["total"]
	if total.Data.(int) != 100 {
		t.Fatalf("聚合数值 = %v，期望 100", total.Data)
	}
	if got := total.Proof.Count(); got != 2 {
		t.Fatalf("10 行同属 2 个主体，聚合主体数 = %d，期望 2", got)
	}
	if policy.CanRelease(total) {
		t.Fatal("行数为 10 但不同主体只有 2 个，不能去标")
	}

	// 加入第三个主体的一行后，主体并集达到 3，行数不再是判定依据。
	raw.Rows = append(raw.Rows, Row{"amount": NewValue(1, "carol")})
	total = Aggregate(raw, "amount", "total", SumInts).Rows[0]["total"]
	if total.Proof.Count() != 3 || !policy.CanRelease(total) {
		t.Fatalf("加入 carol 后主体数 = %d 且应可去标", total.Proof.Count())
	}
}

// 反例 4：聚合之后再过滤。
// 分组 A、B 各自达到 K=3 可去标；过滤删掉 B 后再做总计聚合，
// 真实存活主体只剩 A 组的 3 个——这里进一步构造过滤后不足 K 的情形，
// 证明引擎不能沿用过滤前“已去标”的粘性结论或按过滤前行数放行。
func TestFilterAfterAggregateReevaluatesOnSurvivingProof(t *testing.T) {
	policy := mustPolicy(t, 3)
	raw := NewTable([]string{"grp", "amount"},
		// 组 A：alice、bob 两个主体，多行。
		Row{"grp": NewValue("A", "alice"), "amount": NewValue(1, "alice")},
		Row{"grp": NewValue("A", "alice"), "amount": NewValue(1, "alice")},
		Row{"grp": NewValue("A", "bob"), "amount": NewValue(1, "bob")},
		// 组 B：carol、dave、erin 三个不同主体。
		Row{"grp": NewValue("B", "carol"), "amount": NewValue(1, "carol")},
		Row{"grp": NewValue("B", "dave"), "amount": NewValue(1, "dave")},
		Row{"grp": NewValue("B", "erin"), "amount": NewValue(1, "erin")},
	)

	grouped := GroupAggregate(raw, []string{"grp"}, "amount", "subtotal", SumInts)
	if len(grouped.Rows) != 2 {
		t.Fatalf("分组数 = %d，期望 2", len(grouped.Rows))
	}

	// 组 B 聚合时确实有 3 个不同主体，可以去标；组 A 只有 2 个，不能。
	for _, row := range grouped.Rows {
		subtotal := row["subtotal"]
		switch row["grp"].Data {
		case "A":
			if policy.CanRelease(subtotal) {
				t.Fatal("组 A 只有 2 个主体，聚合后仍应敏感")
			}
		case "B":
			if !policy.CanRelease(subtotal) {
				t.Fatal("组 B 有 3 个不同主体，聚合后应可去标")
			}
		}
	}

	// 过滤只保留组 B（删掉组 A 的行），再对存活的聚合结果做总计。
	onlyB := Filter(grouped, func(row Row) bool { return row["grp"].Data == "B" })
	grand := Aggregate(onlyB, "subtotal", "grand_total", SumInts).Rows[0]["grand_total"]
	if got := grand.Proof.Count(); got != 3 {
		t.Fatalf("过滤后总计的真实主体数 = %d，期望 3", got)
	}

	// 关键反例：若策略收紧到 K=4，过滤前两组合计 5 个主体看似够数，
	// 但过滤后只剩组 B 的 3 个。逐值证明必须判定为敏感，
	// 而不能因为“聚合阶段曾经去标”或按过滤前行数而放行。
	strict := mustPolicy(t, 4)
	if strict.CanRelease(grand) {
		t.Fatal("过滤后仅 3 个存活主体，K=4 下必须仍带敏感标签")
	}
	released, sensitive := Materialize(
		NewTable([]string{"grand_total"}, Row{"grand_total": grand}), strict)
	if len(released) != 0 || len(sensitive) != 1 {
		t.Fatalf("Materialize 放行 %d 行、敏感 %d 行，期望 0/1", len(released), len(sensitive))
	}

	// 过滤不得改写保留行自带的证明：组 B 行在过滤前后主体集合一致。
	survivor := onlyB.Rows[0]["subtotal"]
	if got := survivor.Proof.Count(); got != 3 {
		t.Fatalf("过滤保留行的主体数被改写为 %d，期望 3", got)
	}
}

func mustPolicy(t *testing.T, k int) Policy {
	t.Helper()
	policy, err := NewPolicy(k)
	if err != nil {
		t.Fatalf("NewPolicy(%d) 意外报错: %v", k, err)
	}
	return policy
}
