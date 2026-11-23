package keyrecover

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func TestGFAndSBoxVectors(t *testing.T) {
	if got := gmul(0x53, 0xca); got != 0x01 {
		t.Fatalf("gmul(0x53,0xca)=%#04x, want 0x01", got)
	}
	cases := map[byte]byte{
		0x00: 0x63, 0x01: 0x7c, 0x02: 0x77, 0x10: 0xca,
		0x53: 0xed, 0xff: 0x16,
	}
	for in, want := range cases {
		if got := SBox(in); got != want {
			t.Fatalf("SBox(%#04x)=%#04x, want %#04x", in, got, want)
		}
		if got := InvSBox(want); got != in {
			t.Fatalf("InvSBox(%#04x)=%#04x, want %#04x", want, got, in)
		}
	}
	for x := 0; x < 256; x++ {
		if InvSBox(SBox(byte(x))) != byte(x) {
			t.Fatalf("S 盒在 %#04x 不可逆", x)
		}
	}
}

func TestInvDiffTable(t *testing.T) {
	for alpha := 1; alpha < 256; alpha++ {
		for beta := 1; beta < 256; beta++ {
			for _, x := range invDiffTable[alpha][beta] {
				if SBox(x)^SBox(x^byte(alpha)) != byte(beta) {
					t.Fatalf("差分表项错误 alpha=%#x beta=%#x x=%#x", alpha, beta, x)
				}
			}
		}
		if len(invDiffTable[alpha][0]) != 0 {
			t.Fatalf("alpha=%#x 非零输入差分不应有零输出差分", alpha)
		}
	}
}

func mustSimulate(t *testing.T, key, v [Width]byte, j int, d byte) Observation {
	t.Helper()
	o, err := Simulate(key, v, j, d)
	if err != nil {
		t.Fatalf("Simulate 失败: %v", err)
	}
	return o
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand 失败: %v", err)
	}
	return b
}

func randomState(t *testing.T) [Width]byte {
	var v [Width]byte
	copy(v[:], randomBytes(t, Width))
	return v
}

func containsKey(list []Candidate, key [Width]byte) bool {
	for _, c := range list {
		if c.Key == key {
			return true
		}
	}
	return false
}

func assertWitnesses(t *testing.T, observations []Observation, c Candidate) {
	t.Helper()
	if len(c.Witness) == 0 {
		t.Fatalf("候选 %x 缺少见证", c.Key)
	}
	seenGroups := map[int]bool{}
	for _, w := range c.Witness {
		if w.J < 0 || w.J >= Width || w.D == 0 {
			t.Fatalf("非法见证 %+v", w)
		}
		if seenGroups[w.Group] {
			t.Fatalf("组 %d 出现重复见证", w.Group)
		}
		seenGroups[w.Group] = true
		if !Explains(observations[w.Group], c.Key, w.J, w.D) {
			t.Fatalf("见证 %+v 无法解释组观测", w)
		}
	}
}

func TestSyntheticRecoveryUnique(t *testing.T) {
	key := [Width]byte{0x2b, 0x7e, 0x15, 0x16}
	var obs []Observation
	for g := 0; g < 6; g++ {
		v := randomState(t)
		j := g % Width
		d := byte(1 + (g * 37 % 255))
		obs = append(obs, mustSimulate(t, key, v, j, d))
	}
	result, err := Recover(obs)
	if err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}
	if result.Status != StatusUnique {
		t.Fatalf("状态=%s, 候选数=%d, 期望 unique", result.Status, len(result.Candidates))
	}
	got := result.Candidates[0].Key
	if got != key {
		t.Fatalf("恢复密钥=%x, 期望 %x", got, key)
	}
	assertWitnesses(t, obs, result.Candidates[0])
	if len(result.InformativeGroups) != 6 || len(result.SkippedZeroGroups) != 0 {
		t.Fatalf("分组归类错误: %+v", result)
	}
}

func TestSingleByteInvalidFaultIsInconsistent(t *testing.T) {
	// 单字节差分在 M 的列结构下无法由任何非零 (j,d) 产生。
	obs := []Observation{{
		C: [Width]byte{0x11, 0x22, 0x33, 0x44},
		F: [Width]byte{0xaa, 0x22, 0x33, 0x44},
	}}
	result, err := Recover(obs)
	if err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}
	if result.Status != StatusInconsistent || len(result.Candidates) != 0 {
		t.Fatalf("期望模型不一致, 得到 status=%s candidates=%d",
			result.Status, len(result.Candidates))
	}
}

func TestDuplicateObservations(t *testing.T) {
	key := [Width]byte{0x01, 0x02, 0x03, 0x04}
	o := mustSimulate(t, key, [Width]byte{0x10, 0x20, 0x30, 0x40}, 2, 0x77)
	observations := []Observation{o, o, o}
	// 单种观测在全字节域上约束不足，用包含真实密钥的小域验证重复不丢解。
	domains := [Width][]byte{
		{0x00, 0x01, 0x80, 0xff},
		{0x02, 0x10, 0x44, 0xab},
		{0x03, 0x22, 0x55, 0xcd},
		{0x04, 0x33, 0x66, 0xef},
	}
	result, err := Recover(observations, WithDomains(domains))
	if err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}
	if !containsKey(result.Candidates, key) {
		t.Fatalf("重复观测后真实密钥未保留, status=%s", result.Status)
	}
	for _, c := range result.Candidates {
		assertWitnesses(t, observations, c)
	}
	// 重复约束不应产生重复见证组。
	for _, c := range result.Candidates {
		if len(c.Witness) != 3 {
			t.Fatalf("期望每组各一条见证, 得到 %d 条", len(c.Witness))
		}
	}
}

func TestZeroFaultInputs(t *testing.T) {
	key := [Width]byte{0xab, 0xcd, 0xef, 0x01}
	zero := mustSimulate(t, key, randomState(t), 0, 0)

	// 仅零故障：跳过全部观测，可行域为全字节域，不唯一且不展开。
	result, err := Recover([]Observation{zero})
	if !errors.Is(err, ErrCandidateLimit) {
		t.Fatalf("期望 ErrCandidateLimit, 得到 %v", err)
	}
	if len(result.SkippedZeroGroups) != 1 || len(result.InformativeGroups) != 0 {
		t.Fatalf("零故障分组归类错误: %+v", result)
	}
	for p := 0; p < Width; p++ {
		if len(result.PrunedDomain[p]) != 256 {
			t.Fatalf("字节 %d 剪枝域大小=%d, 期望 256", p, len(result.PrunedDomain[p]))
		}
	}

	// 零故障与有效故障混合：零故障被跳过，有效观测仍唯一恢复。
	good := mustSimulate(t, key, randomState(t), 1, 0x2b)
	var more []Observation
	for g := 0; g < 5; g++ {
		more = append(more, mustSimulate(t, key, randomState(t), g%Width, byte(1+g*13)))
	}
	mixed := append([]Observation{zero, good}, more...)
	result2, err := Recover(mixed)
	if err != nil {
		t.Fatalf("混合观测 Recover 失败: %v", err)
	}
	if result2.Status != StatusUnique || result2.Candidates[0].Key != key {
		t.Fatalf("混合观测恢复错误: status=%s", result2.Status)
	}
	if len(result2.SkippedZeroGroups) != 1 || result2.SkippedZeroGroups[0] != 0 {
		t.Fatalf("零故障下标错误: %v", result2.SkippedZeroGroups)
	}
	if len(result2.InformativeGroups) != 6 {
		t.Fatalf("有效分组数=%d, 期望 6", len(result2.InformativeGroups))
	}
}

// bruteForceFeasible 在每字节 4 个允许值的小域上做全组合枚举（4^4=256），
// 直接按定义判断可行性，作为候选生成算法的独立参照实现。
func bruteForceFeasible(t *testing.T, observations []Observation, domains [Width][]byte) map[[Width]byte][][2]int {
	t.Helper()
	domainBits := [Width]bitset{}
	for p := 0; p < Width; p++ {
		domainBits[p] = toBitset(domains[p])
	}
	feasible := map[[Width]byte][][2]int{}
	var key [Width]byte
	var walk func(pos int)
	walk = func(pos int) {
		if pos == Width {
			allOK := true
			var witness [][2]int
			for _, o := range observations {
				zero := true
				for p := 0; p < Width; p++ {
					if o.C[p] != o.F[p] {
						zero = false
					}
				}
				if zero {
					continue
				}
				found := false
				for j := 0; j < Width && !found; j++ {
					for d := 1; d <= 255; d++ {
						if Explains(o, key, j, byte(d)) {
							witness = append(witness, [2]int{j, d})
							found = true
							break
						}
					}
				}
				if !found {
					allOK = false
					break
				}
			}
			if allOK {
				feasible[key] = witness
			}
			return
		}
		for _, v := range domains[pos] {
			if !domainBits[pos].contains(v) {
				continue
			}
			key[pos] = v
			walk(pos + 1)
		}
	}
	walk(0)
	return feasible
}

func TestSmallDomainBruteForceCrossValidation(t *testing.T) {
	const trials = 40
	for trial := 0; trial < trials; trial++ {
		// 每字节仅 4 个允许值的小域，包含真实密钥字节。
		var domains [Width][]byte
		var key [Width]byte
		for p := 0; p < Width; p++ {
			// 确定性地取 4 个互不相同的允许值，并保证包含真实密钥字节。
			domain := map[byte]bool{}
			seed := byte(trial*31 + p*17)
			for k := 0; len(domain) < 4; k++ {
				domain[seed^byte(k*53+p*5)] = true
			}
			for v := range domain {
				domains[p] = append(domains[p], v)
			}
			domains[p] = dedupeSorted(sortedBytes(domains[p]))
			key[p] = domains[p][trial%len(domains[p])]
		}

		nObs := 1 + trial%4
		observations := make([]Observation, nObs)
		for g := range observations {
			observations[g] = mustSimulate(t, key, randomState(t),
				(g+trial)%Width, byte(1+(trial*7+g*11)%255))
		}

		result, err := Recover(observations, WithDomains(domains))
		if err != nil {
			t.Fatalf("trial %d Recover 失败: %v", trial, err)
		}
		want := bruteForceFeasible(t, observations, domains)
		if len(result.Candidates) != len(want) {
			t.Fatalf("trial %d 候选数=%d, 暴力参照=%d", trial, len(result.Candidates), len(want))
		}
		for _, c := range result.Candidates {
			if _, ok := want[c.Key]; !ok {
				t.Fatalf("trial %d 算法候选 %x 不在暴力可行集中", trial, c.Key)
			}
			assertWitnesses(t, observations, c)
		}
		switch len(want) {
		case 0:
			if result.Status != StatusInconsistent {
				t.Fatalf("trial %d 期望 inconsistent", trial)
			}
		case 1:
			if result.Status != StatusUnique {
				t.Fatalf("trial %d 期望 unique", trial)
			}
		default:
			if result.Status != StatusAmbiguous {
				t.Fatalf("trial %d 期望 ambiguous", trial)
			}
		}
	}
}

func TestDomainRestrictionExcludesKey(t *testing.T) {
	key := [Width]byte{0x10, 0x20, 0x30, 0x40}
	var obs []Observation
	for g := 0; g < 5; g++ {
		obs = append(obs, mustSimulate(t, key, randomState(t), g%Width, byte(2+g)))
	}
	var domains [Width][]byte
	for p := 0; p < Width; p++ {
		// 故意排除真实字节。
		for v := 0; v < 8; v++ {
			if byte(v) != key[p] {
				domains[p] = append(domains[p], byte(v))
			}
		}
	}
	result, err := Recover(obs, WithDomains(domains))
	if err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}
	if result.Status != StatusInconsistent {
		t.Fatalf("排除真实密钥后应不一致, 得到 %s / %d 候选", result.Status, len(result.Candidates))
	}
}

func TestEmptyDomainAndCandidateLimit(t *testing.T) {
	key := [Width]byte{0x55, 0x66, 0x77, 0x88}
	obs := []Observation{mustSimulate(t, key, randomState(t), 0, 0x09)}

	var empty [Width][]byte
	empty[0] = []byte{} // 显式空域：区别于“未提供 -> 全字节域”
	if _, err := Recover(obs, WithDomains(empty)); !errors.Is(err, ErrEmptyDomain) {
		t.Fatalf("期望 ErrEmptyDomain, 得到 %v", err)
	}

	// 无信息零故障 + 8 值小域：可行集即 8^4=4096，超过候选上限。
	zeroObs := mustSimulate(t, key, randomState(t), 0, 0)
	var wide [Width][]byte
	for p := range wide {
		for v := 0; v < 8; v++ {
			wide[p] = append(wide[p], byte(v*17+p))
		}
	}
	result, err := Recover([]Observation{zeroObs}, WithDomains(wide), WithMaxCandidates(32))
	if !errors.Is(err, ErrCandidateLimit) {
		t.Fatalf("期望 ErrCandidateLimit, 得到 %v", err)
	}
	if result.Candidates != nil {
		t.Fatalf("触发上限时不应展开候选")
	}
	for p := 0; p < Width; p++ {
		if len(result.PrunedDomain[p]) == 0 {
			t.Fatalf("字节 %d 剪枝域为空", p)
		}
	}
}

func TestCandidatesSortedByKey(t *testing.T) {
	key := [Width]byte{0x0f, 0x1e, 0x2d, 0x3c}
	// 零故障不施加约束，候选集即小域全组合，可验证字节序与见证缺省。
	o := mustSimulate(t, key, randomState(t), 0, 0)
	domains := [Width][]byte{
		{0x00, 0x0f, 0x31, 0x70, 0xa5, 0xff},
		{0x01, 0x1e, 0x42, 0x81, 0xb6, 0xee},
		{0x02, 0x2d, 0x53, 0x92, 0xc7, 0xdd},
		{0x03, 0x3c, 0x64, 0xa3, 0xd8, 0xcc},
	}
	result, err := Recover([]Observation{o}, WithDomains(domains))
	if err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}
	if len(result.Candidates) < 2 {
		t.Fatalf("单组观测应保留多候选, 得到 %d", len(result.Candidates))
	}
	for i := 1; i < len(result.Candidates); i++ {
		if bytes.Compare(result.Candidates[i-1].Key[:], result.Candidates[i].Key[:]) >= 0 {
			t.Fatalf("候选未按字节序升序: %x >= %x",
				result.Candidates[i-1].Key, result.Candidates[i].Key)
		}
	}
}
