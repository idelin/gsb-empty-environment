package netsec

import (
	"errors"
	"testing"
)

// universeBits 是枚举测试使用的能力全集位数。3 位能力给出 8 种集合，
// 进程/文件状态组合在秒级内可穷举。
const universeBits = 3

func universeMasks() []Set {
	masks := make([]Set, 0, 1<<universeBits)
	for m := 0; m < 1<<universeBits; m++ {
		masks = append(masks, Set(m))
	}
	return masks
}

// validProcesses 枚举满足内核不变量（A⊆P、E⊆P、A⊆B）的全部进程状态。
func validProcesses(nnp bool) []Process {
	masks := universeMasks()
	var out []Process
	for _, p := range masks {
		for _, i := range masks {
			for _, a := range masks {
				if !a.IsSubset(p) {
					continue
				}
				for _, b := range masks {
					if !a.IsSubset(b) {
						continue
					}
					for _, e := range masks {
						if !e.IsSubset(p) {
							continue
						}
						out = append(out, Process{
							Permitted:   p,
							Inheritable: i,
							Ambient:     a,
							Bounding:    b,
							Effective:   e,
							NoNewPrivs:  nnp,
						})
					}
				}
			}
		}
	}
	return out
}

// allFiles 枚举 3 位全集下的全部文件状态（含映射失败与各种特权位）。
func allFiles() []File {
	masks := universeMasks()
	var out []File
	for _, fp := range masks {
		for _, fi := range masks {
			for _, eff := range []bool{false, true} {
				for _, suid := range []bool{false, true} {
					for _, sgid := range []bool{false, true} {
						for _, mapped := range []bool{false, true} {
							out = append(out, File{
								Permitted:           fp,
								Inheritable:         fi,
								Effective:           eff,
								SetUID:              suid,
								SetGID:              sgid,
								UserNamespaceMapped: mapped,
							})
						}
					}
				}
			}
		}
	}
	return out
}

func assertResultInvariants(t *testing.T, prev, next Process) {
	t.Helper()
	if !next.Ambient.IsSubset(next.Permitted) {
		t.Fatalf("ambient %v not subset of permitted %v", next.Ambient, next.Permitted)
	}
	if !next.Effective.IsSubset(next.Permitted) {
		t.Fatalf("effective %v not subset of permitted %v", next.Effective, next.Permitted)
	}
	if !next.Ambient.IsSubset(next.Bounding) {
		t.Fatalf("ambient %v not subset of bounding %v", next.Ambient, next.Bounding)
	}
	if next.Inheritable != prev.Inheritable {
		t.Fatalf("inheritable set changed across exec: %v -> %v", prev.Inheritable, next.Inheritable)
	}
	if next.Bounding != prev.Bounding {
		t.Fatalf("bounding set changed across exec: %v -> %v", prev.Bounding, next.Bounding)
	}
	if next.NoNewPrivs != prev.NoNewPrivs || next.SecureBits != prev.SecureBits {
		t.Fatalf("security flags changed across exec")
	}
}

// TestExecveExhaustive 对全集内所有合法进程状态 × 所有文件状态做枚举，
// 逐组核对转换公式、内核不变量、no_new_privs 收窄与重复执行幂等性。
func TestExecveExhaustive(t *testing.T) {
	files := allFiles()
	for _, nnp := range []bool{false, true} {
		for _, proc := range validProcesses(nnp) {
			for _, f := range files {
				got, err := proc.Execve(f)
				if err != nil {
					t.Fatalf("Execve(%+v, %+v): unexpected error %v", proc, f, err)
				}
				want := transition(proc, f)
				if got != want {
					t.Fatalf("Execve(%+v, %+v) = %+v, want %+v", proc, f, got, want)
				}
				assertResultInvariants(t, proc, got)

				// 同一文件再次执行必须幂等：I、B 不变，ambient 已收敛。
				again, err := got.Execve(f)
				if err != nil {
					t.Fatalf("second Execve error %v", err)
				}
				if again != got {
					t.Fatalf("repeated exec not idempotent: %+v -> %+v", got, again)
				}

				if nnp {
					// 禁止提权：文件特权全部忽略，结果只剩原 ambient，
					// 且新 permitted 不得超出旧 permitted。
					if got.Permitted != proc.Ambient || got.Effective != proc.Ambient || got.Ambient != proc.Ambient {
						t.Fatalf("no_new_privs not narrowed to ambient: %+v -> %+v", proc, got)
					}
					if !got.Permitted.IsSubset(proc.Permitted) {
						t.Fatalf("no_new_privs escalation: %v not subset of %v", got.Permitted, proc.Permitted)
					}
				}
			}
		}
	}
}

// TestTransitionFormula 用命名场景固定 man page 公式的关键分支。
func TestTransitionFormula(t *testing.T) {
	// P' = (I ∩ F_i) ∪ (F_p ∩ B) ∪ A；effective 标记置位时 E' = P'。
	proc := Process{
		Permitted:   0b111,
		Inheritable: 0b101,
		Ambient:     0b001,
		Bounding:    0b111,
		Effective:   0b111,
	}
	f := File{
		Permitted:           0b110,
		Inheritable:         0b101,
		Effective:           true,
		UserNamespaceMapped: true,
	}
	got, err := proc.Execve(f)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	// (I∩F_i)=0b101, (F_p∩B)=0b110, A=0b001 → 0b111；普通文件保留 A。
	if got.Permitted != 0b111 || got.Effective != 0b111 {
		t.Fatalf("effective-bit case got P=%v E=%v", got.Permitted, got.Effective)
	}

	// effective 标记清零：E' 只取 ambient。
	f.Effective = false
	got, err = proc.Execve(f)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	// 文件有能力但未置 effective 标记：P' 不变，E' 为空（ambient 的
	// 自动生效只发生在无文件能力的普通文件上）。
	if got.Permitted != 0b111 || got.Effective != 0 {
		t.Fatalf("non-effective case got P=%v E=%v, want empty effective", got.Permitted, got.Effective)
	}

	// 特权文件（setuid）先清空 ambient：A'=0，结果不含 ambient 项。
	priv := File{SetUID: true, UserNamespaceMapped: true}
	got, err = proc.Execve(priv)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	if got.Ambient != 0 || got.Permitted != 0 || got.Effective != 0 {
		t.Fatalf("privileged file did not drop ambient: %+v", got)
	}

	// 特权文件携带文件能力：ambient 清空，P' 仅由文件两项构成。
	priv = File{
		Permitted:           0b010,
		Inheritable:         0b101,
		Effective:           true,
		SetGID:              true,
		UserNamespaceMapped: true,
	}
	got, err = proc.Execve(priv)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	wantP := proc.Inheritable.Intersect(priv.Inheritable).Union(priv.Permitted.Intersect(proc.Bounding))
	if got.Ambient != 0 || got.Permitted != wantP || got.Effective != wantP {
		t.Fatalf("privileged file caps case: got %+v, want P=%v", got, wantP)
	}
}

// TestUnmappedFileCaps 核对用户命名空间映射失败按“无文件能力”处理。
func TestUnmappedFileCaps(t *testing.T) {
	proc := Process{
		Permitted:   0b111,
		Inheritable: 0b101,
		Ambient:     0b001,
		Bounding:    0b111,
		Effective:   0b111,
	}

	// 映射失败：任意文件能力与 effective 标记都被忽略，等价于空能力文件。
	unmapped := File{
		Permitted:           0b110,
		Inheritable:         0b101,
		Effective:           true,
		UserNamespaceMapped: false,
	}
	reference := File{UserNamespaceMapped: true}
	got, err := proc.Execve(unmapped)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	want, err := proc.Execve(reference)
	if err != nil {
		t.Fatalf("reference Execve: %v", err)
	}
	if got != want {
		t.Fatalf("unmapped file caps %+v != empty-caps reference %+v", got, want)
	}
	if got.Permitted != proc.Ambient || got.Effective != proc.Ambient {
		t.Fatalf("unmapped file should preserve ambient only, got %+v", got)
	}

	// 映射失败但置了 setuid：仍是特权文件，ambient 被清空，能力全空。
	unmapped.SetUID = true
	got, err = proc.Execve(unmapped)
	if err != nil {
		t.Fatalf("Execve: %v", err)
	}
	if got.Ambient != 0 || got.Permitted != 0 || got.Effective != 0 {
		t.Fatalf("unmapped setuid file should yield empty sets: %+v", got)
	}

	// EffectiveFileCaps 直接返回空集合。
	fp, fi, eff := unmapped.EffectiveFileCaps()
	if fp != 0 || fi != 0 || eff {
		t.Fatalf("EffectiveFileCaps on unmapped file: %v %v %v", fp, fi, eff)
	}
}

// TestRepeatedExecChains 用确定性 LCG 生成多次连续执行链，核对每一步都
// 满足不变量，且链尾对同一文件重复执行达到稳定状态。
func TestRepeatedExecChains(t *testing.T) {
	files := allFiles()
	procs := append(validProcesses(false), validProcesses(true)...)
	seed := uint64(0x9e3779b97f4a7c15)
	nextRand := func() int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(seed >> 33)
	}
	for chain := 0; chain < 64; chain++ {
		cur := procs[nextRand()%len(procs)]
		for step := 0; step < 200; step++ {
			f := files[nextRand()%len(files)]
			next, err := cur.Execve(f)
			if err != nil {
				t.Fatalf("chain %d step %d: %v", chain, step, err)
			}
			assertResultInvariants(t, cur, next)
			cur = next
		}
		// 链尾固定文件，连续执行三次，状态必须保持稳定。
		f := files[nextRand()%len(files)]
		first, err := cur.Execve(f)
		if err != nil {
			t.Fatalf("tail exec: %v", err)
		}
		second, err := first.Execve(f)
		if err != nil {
			t.Fatalf("tail exec: %v", err)
		}
		third, err := second.Execve(f)
		if err != nil {
			t.Fatalf("tail exec: %v", err)
		}
		if first != second || second != third {
			t.Fatalf("chain tail did not stabilize: %+v %+v %+v", first, second, third)
		}
	}
}

// TestFilePrivileged 核对特权文件判定：setuid/setgid 位与映射成功的
// 文件能力算特权；映射失败的文件能力不算。
func TestFilePrivileged(t *testing.T) {
	cases := []struct {
		name string
		f    File
		want bool
	}{
		{"plain", File{UserNamespaceMapped: true}, false},
		{"setuid", File{SetUID: true, UserNamespaceMapped: true}, true},
		{"setgid", File{SetGID: true, UserNamespaceMapped: true}, true},
		{"file caps", File{Permitted: Set(1), UserNamespaceMapped: true}, true},
		{"inheritable caps", File{Inheritable: Set(1), UserNamespaceMapped: true}, true},
		{"unmapped caps ignored", File{Permitted: Set(1), Inheritable: Set(1), UserNamespaceMapped: false}, false},
		{"unmapped but setuid", File{Permitted: Set(1), SetUID: true, UserNamespaceMapped: false}, true},
	}
	for _, tc := range cases {
		if got := tc.f.Privileged(); got != tc.want {
			t.Fatalf("%s: Privileged() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestInvalidProcessRejected 核对违反内核不变量的输入被拒绝。
func TestInvalidProcessRejected(t *testing.T) {
	cases := []Process{
		{Permitted: 0b001, Ambient: 0b010, Bounding: 0b111},   // A 不是 P 子集
		{Permitted: 0b001, Effective: 0b010, Bounding: 0b111}, // E 不是 P 子集
		{Permitted: 0b111, Ambient: 0b010, Bounding: 0b001},   // A 不是 B 子集
	}
	for idx, bad := range cases {
		if _, err := bad.Execve(File{UserNamespaceMapped: true}); !errors.Is(err, ErrInvariantViolation) {
			t.Fatalf("case %d: got %v, want ErrInvariantViolation", idx, err)
		}
	}
}
