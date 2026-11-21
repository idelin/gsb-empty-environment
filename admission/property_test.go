package admission

import (
	"errors"
	"testing"
)

// 反拼接：一条历史拥有可信内容，另一条历史在同位置只有权限更新，
// 绝不允许把前者的内容和后者的签名范围合并成“确定可执行”。
func TestNoCrossHistorySplicing(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R-content", Range{0, 3}),
		permReceipt("vendor", "R0", "R-perm", Range{0, 7}, Range{4, 7}),
	}
	in := baseInput(receipts,
		[][]byte{root("R-content"), root("R-perm")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictUnknown:    {{0, 3}},
		VerdictForbidden:  {{4, 7}},
		VerdictExecutable: {{8, smallDomainPages - 1}},
	}
	assertVerdicts(t, got, want)
}

// 共享分支：两条历史共享同一段前缀更新，前缀上的确定性结论必须保留。
func TestSharedBranchPrefix(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R1", Range{0, 3}),
		contentReceipt("vendor", "R1", "R2a", Range{4, 5}),
		contentReceipt("vendor", "R1", "R2b", Range{6, 7}),
	}
	in := baseInput(receipts, [][]byte{root("R2a"), root("R2b")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictUnknown:    {{4, 7}},
		VerdictExecutable: {{0, 3}, {8, smallDomainPages - 1}},
	}
	assertVerdicts(t, got, want)
}

// 被拒绝签名的收据不能支撑任何确定可执行结论；执行位仍在，故为未知。
func TestRejectedSignature(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("bad", "R0", "R1", Range{0, 3}),
	}
	in := baseInput(receipts, [][]byte{root("R1")})
	got := runAdmit(t, in)

	want := map[Verdict][]Range{
		VerdictUnknown:    {{0, 3}},
		VerdictExecutable: {{4, smallDomainPages - 1}},
	}
	assertVerdicts(t, got, want)
}

// 48 位边界：请求区间直达 MaxPage，实现不得逐页展开或发生溢出。
func TestFortyEightBitBoundary(t *testing.T) {
	cp := Checkpoint{
		Root:        root("R0"),
		Shards:      []Range{{0, MaxPage}},
		Permissions: fullPerm(PermExec),
		Base: []BaseContent{{
			Pages:  Range{0, MaxPage},
			Digest: digest("base"),
			Trust:  TrustTrusted,
		}},
	}
	receipts := []Receipt{
		permReceipt("vendor", "R0", "R1",
			Range{MaxPage - 3, MaxPage}, Range{MaxPage - 3, MaxPage}),
	}
	in := AdmissionInput{
		Checkpoint: cp,
		Receipts:   receipts,
		Heads:      [][]byte{root("R1")},
		Request:    Range{MaxPage - 10, MaxPage},
		Verifier:   fakeVerifier{trusted: map[string]bool{"vendor": true}},
	}
	got := runAdmit(t, in)
	want := map[Verdict][]Range{
		VerdictExecutable: {{MaxPage - 10, MaxPage - 4}},
		VerdictForbidden:  {{MaxPage - 3, MaxPage}},
	}
	assertVerdicts(t, got, want)
}

// 默认叶根：未显式给头时，所有 DAG 叶节点都成为候选根。
func TestDefaultLeafHeads(t *testing.T) {
	receipts := []Receipt{
		contentReceipt("vendor", "R0", "R1", Range{0, 3}),
		contentReceipt("vendor", "R1", "R2", Range{4, 5}),
	}
	in := baseInput(receipts, nil)
	a, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	heads := a.Heads()
	if len(heads) != 1 || string(heads[0]) != "R2" {
		t.Fatalf("default heads = %v, want [R2]", heads)
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*AdmissionInput)
		want error
	}{
		{"missing verifier", func(in *AdmissionInput) { in.Verifier = nil },
			ErrMissingVerifier},
		{"bad request", func(in *AdmissionInput) {
			in.Request = Range{Start: 9, End: 1}
		}, ErrBadRequest},
		{"bad shards", func(in *AdmissionInput) {
			in.Checkpoint.Shards = []Range{{0, 7}}
		}, ErrBadShards},
		{"permission gap", func(in *AdmissionInput) {
			in.Checkpoint.Permissions = []PermissionRange{
				{Pages: Range{0, 7}, Perm: PermExec},
			}
		}, ErrBadPermissions},
		{"unaligned content receipt", func(in *AdmissionInput) {
			// 三分片下，[0,2] 跨越分片边界但端点不是分片边，必须被拒绝。
			in.Checkpoint.Shards = []Range{
				{0, 1},
				{2, 3},
				{4, MaxPage},
			}
			in.Checkpoint.Base = []BaseContent{{
				Pages:  Range{0, 1},
				Digest: digest("base"),
				Trust:  TrustTrusted,
			}}
			in.Receipts = []Receipt{
				contentReceipt("vendor", "R0", "R1", Range{0, 2}),
			}
			in.Heads = [][]byte{root("R1")}
		}, ErrBadReceipt},
		{"unreachable head", func(in *AdmissionInput) {
			in.Heads = [][]byte{root("ghost")}
		}, ErrUnreachable},
		{"unreachable receipt", func(in *AdmissionInput) {
			in.Receipts = []Receipt{
				contentReceipt("vendor", "X0", "X1", Range{0, 3}),
			}
		}, ErrUnreachable},
		{"root conflict", func(in *AdmissionInput) {
			r1 := contentReceipt("vendor", "R0", "R1", Range{0, 3})
			r2 := r1
			r2.Publisher = "other"
			in.Receipts = []Receipt{r1, r2}
			in.Heads = [][]byte{root("R1")}
		}, ErrRootConflict},
		{"bad permission receipt", func(in *AdmissionInput) {
			in.Receipts = []Receipt{{
				Publisher: "vendor",
				OldRoot:   root("R0"),
				NewRoot:   root("R1"),
				Pages:     Range{0, 3},
				Kind:      KindPermission,
			}}
			in.Heads = [][]byte{root("R1")}
		}, ErrBadReceipt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput(nil, [][]byte{root("R0")})
			tc.mut(&in)
			_, err := Build(in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
