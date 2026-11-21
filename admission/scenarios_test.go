package admission

import "testing"

// 场景一：并行增量的提交确认丢失。显式候选根包含“更新尚未持久化”的
// 检查点根，因此被更新位置不能被确定为新内容。
func TestLostCommitConfirmation(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R1", Range{0, 3}),
	}
	in := baseInput(receipts, [][]byte{root("R0"), root("R1")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		// 0-3 在 R0 历史是旧内容、R1 历史是新内容：跨历史内容不一致。
		VerdictUnknown:    {{0, 3}},
		VerdictExecutable: {{4, smallDomainPages - 1}},
		VerdictForbidden:  nil,
	}
	assertVerdicts(t, got, want)

	// 一旦持久化确定（只剩 R1），同一位置立即变为确定可执行。
	in.Heads = [][]byte{root("R1")}
	got = runAdmit(t, in)
	want = map[Verdict][]Range{
		VerdictExecutable: {{0, smallDomainPages - 1}},
	}
	assertVerdicts(t, got, want)
}

// 场景二：别名页共享内容但权限属于位置。两个位置更新为同一摘要，
// 随后只撤销其中一个位置的执行权；另一个位置必须保持确定可执行。
func TestAliasSameContentDifferentPermission(t *testing.T) {
	shared := digest("shared")
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R1", Range{0, 7},
			ContentEntry{Pages: Range{0, 3}, Digest: shared},
			ContentEntry{Pages: Range{4, 7}, Digest: shared}),
		permReceipt("vendor", "R1", "R2", Range{0, 3}, Range{0, 3}),
	}
	in := baseInput(receipts, [][]byte{root("R2")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictForbidden:  {{0, 3}},
		VerdictExecutable: {{4, smallDomainPages - 1}},
		VerdictUnknown:    nil,
	}
	assertVerdicts(t, got, want)
}

// 场景三：只撤销半区间。权限收据覆盖 0-7 但仅撤销 0-3，
// 判定必须精确到页，另一半仍确定可执行。
func TestPartialRevocation(t *testing.T) {
	receipts := []Receipt{
		permReceipt("vendor", "R0", "R1", Range{0, 7}, Range{0, 3}),
	}
	in := baseInput(receipts, [][]byte{root("R1")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictForbidden:  {{0, 3}},
		VerdictExecutable: {{4, smallDomainPages - 1}},
	}
	assertVerdicts(t, got, want)
}

// 场景四：旁支上的不可信缺口不得冻结已安全确定的主支。
// 旁支把 8-11 换成不可信发布者的内容；旁支完全未触及的 12-15
// 必须仍确定可执行，而 8-11 因历史间分歧只能判未知。
func TestSideBranchGapDoesNotFreeze(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R-main", Range{0, 3}),
		contentReceipt("rogue", "R0", "R-side", Range{8, 11}),
	}
	in := baseInput(receipts,
		[][]byte{root("R-main"), root("R-side")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictUnknown:    {{0, 3}, {8, 11}},
		VerdictExecutable: {{4, 7}, {12, smallDomainPages - 1}},
		VerdictForbidden:  nil,
	}
	assertVerdicts(t, got, want)
}
