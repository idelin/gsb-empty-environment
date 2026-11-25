package permcompile

import "testing"

// TestLateDisambiguation 两个相同许可的晚消歧：
// 事件到来前同范围读取即允许，提交同时保留两种解释；
// 真实选择事件到达后只保留相容解释，另一分支永久关闭。
func TestLateDisambiguation(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindChoice,
		On:   "pick",
		Kids: []*Node{
			{Kind: KindLeaf, Opt: "a", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 2}},
			{Kind: KindLeaf, Opt: "b", Grant: &Grant{Object: "o", Gen: 1, Scope: ByteRange{0, 10}, MaxReads: 2}},
		},
	}}
	it := newInterp(t, plan)
	it.expectWorlds(1)

	// 晚消歧前：两个相同许可都能服务读取，允许。
	it.allow(req("o", 1, 0, 10))
	// 提交后保留两个暂定世界：每个世界固定一个选项分支并精确扣减该叶，
	// 对侧叶在该解释中被取消，不能任取一个叶超发配额。
	it.expectWorlds(2,
		"L0[active]b=[0:10]r=[1:2]", "L1[canceled]b=Er=E",
		"L0[canceled]b=Er=E", "L1[active]b=[0:10]r=[1:2]")

	// 第二次读取：每个世界固定分支上的叶子仍剩一次，允许且保留双解释。
	it.allow(req("o", 1, 0, 10))
	it.expectWorlds(2, "L0[completed]b=[0:10]r=E", "L1[completed]b=[0:10]r=E")

	// 消歧选择 a：只保留把消费记在 L0 的世界，L1 被取消且配额清空。
	it.ingest(it.decision("pick", "a"))
	it.expectWorlds(1, "L0[completed]b=[0:10]r=E", "L1[canceled]b=Er=E")

	// 已耗尽 + 另一分支取消：永远不能再开读。
	it.deny(req("o", 1, 0, 1))
}

// TestCrossLeafSplice 跨多个相邻区间拼接：单叶不足时由多叶联合覆盖，
// 所有相容拼接方式都保留并分别扣减。
func TestCrossLeafSplice(t *testing.T) {
	plan := &Plan{Root: &Node{
		Kind: KindParallel,
		Kids: []*Node{
			leafGrant("o", 1, 0, 10, 1),
			leafGrant("o", 1, 10, 20, 1),
			leafGrant("o", 1, 5, 15, 1),
		},
	}}
	it := newInterp(t, plan)

	it.allow(req("o", 1, 0, 20))
	snap := it.rt.Snapshot()
	if len(snap) < 2 {
		t.Fatalf("expected multiple splice interpretations, got %d", len(snap))
	}
	for _, ws := range snap {
		used := 0
		for _, l := range ws.Leaves {
			if l.Reads.empty() {
				used++
			}
		}
		if used < 2 {
			t.Fatalf("splice must consume at least two leaves, got %d", used)
		}
	}

	// 无法被任何组合覆盖的空洞必须拒绝。
	it.deny(req("o", 1, 18, 30))
	// 代次不同永不匹配。
	it.deny(req("o", 2, 0, 5))
}

// TestPartialReads 部分读取：字节配额以区间集合精确切分，次数逐次扣减。
func TestPartialReads(t *testing.T) {
	plan := &Plan{Root: leafGrant("o", 1, 0, 100, 3)}
	it := newInterp(t, plan)

	it.allow(req("o", 1, 10, 20))
	it.expectWorlds(1, "L0[active]b=[0:10][20:100]r=[1:3]")

	it.allow(req("o", 1, 50, 60))
	it.expectWorlds(1, "L0[active]b=[0:10][20:50][60:100]r=[2:3]")

	// 跨过已消费空洞的读取无法被完整覆盖，拒绝。
	it.deny(req("o", 1, 15, 25))
	// 最后一次读取后次数耗尽。
	it.allow(req("o", 1, 0, 10))
	it.expectWorlds(1, "L0[completed]b=[0:10][20:50][60:100]r=E")
	it.deny(req("o", 1, 20, 30))
}
