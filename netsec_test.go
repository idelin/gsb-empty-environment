package netsec

import (
	"errors"
	"testing"
)

// domainN 是穷举测试使用的有限哈希域大小，取值为 0..domainN-1。
const domainN = 16

var (
	testParamsA = Digest{1}
	testParamsB = Digest{2}
)

func rec(h, succ Hash) Record {
	return Record{Hash: h, Successor: succ, ZoneVersion: 7, Params: testParamsA}
}

// referenceInterval 在有限域上用逐步走环的方式独立计算开区间成员，
// 作为 inOpenInterval 的参照模型。
func referenceInterval(h, succ, t Hash) bool {
	for p := h; ; {
		p = (p + 1) % domainN
		if p == succ {
			return false
		}
		if p == t {
			return true
		}
	}
}

// TestIntervalExhaustive 在有限域上穷举所有 (h, succ, target) 组合，
// 覆盖普通区间、回绕区间、单节点环（h == succ）与端点情形。
func TestIntervalExhaustive(t *testing.T) {
	for h := Hash(0); h < domainN; h++ {
		for succ := Hash(0); succ < domainN; succ++ {
			for target := Hash(0); target < domainN; target++ {
				err := VerifyNonExistence(target, []Record{rec(h, succ)})
				wantAbsent := referenceInterval(h, succ, target)
				if wantAbsent && err != nil {
					t.Fatalf("h=%d succ=%d target=%d: 应证明不存在，得到 %v", h, succ, target, err)
				}
				if !wantAbsent && !errors.Is(err, ErrNotProven) {
					t.Fatalf("h=%d succ=%d target=%d: 应拒绝，得到 %v", h, succ, target, err)
				}
			}
		}
	}
}

// TestSingleNodeRing 穷举单节点环：除节点自身外所有点都被证明不存在。
func TestSingleNodeRing(t *testing.T) {
	for h := Hash(0); h < domainN; h++ {
		for target := Hash(0); target < domainN; target++ {
			err := VerifyNonExistence(target, []Record{rec(h, h)})
			if target == h {
				if !errors.Is(err, ErrNotProven) {
					t.Fatalf("h=%d target=%d: 同点不应算不存在，得到 %v", h, target, err)
				}
			} else if err != nil {
				t.Fatalf("h=%d target=%d: 单节点环应证明不存在，得到 %v", h, target, err)
			}
		}
	}
}

// TestEmptyZoneClaim 验证空区声明：单独使用时任何目标都不存在，
// 与节点记录或其它记录混用时构成矛盾证据。
func TestEmptyZoneClaim(t *testing.T) {
	empty := Record{ZoneVersion: 7, Params: testParamsA, Empty: true}
	for target := Hash(0); target < domainN; target++ {
		if err := VerifyNonExistence(target, []Record{empty}); err != nil {
			t.Fatalf("target=%d: 空区声明应证明不存在，得到 %v", target, err)
		}
	}

	cases := map[string][]Record{
		"空区+节点": {empty, rec(1, 2)},
		"节点+空区": {rec(1, 2), empty},
		"空区+空区": {empty, empty},
	}
	for name, records := range cases {
		if err := VerifyNonExistence(0, records); !errors.Is(err, ErrEmptyConflict) {
			t.Fatalf("%s: 应报矛盾证据，得到 %v", name, err)
		}
	}
}

// TestEndpoints 穷举验证开区间端点：target 与任一端点重合都不算不存在。
func TestEndpoints(t *testing.T) {
	for h := Hash(0); h < domainN; h++ {
		for succ := Hash(0); succ < domainN; succ++ {
			if h == succ {
				continue
			}
			for _, target := range []Hash{h, succ} {
				if err := VerifyNonExistence(target, []Record{rec(h, succ)}); !errors.Is(err, ErrNotProven) {
					t.Fatalf("h=%d succ=%d target=%d: 端点不应算不存在，得到 %v", h, succ, target, err)
				}
			}
		}
	}
}

// TestConflictSuccessor 穷举同一节点携带不同后继的冲突证据。
func TestConflictSuccessor(t *testing.T) {
	for h := Hash(0); h < domainN; h++ {
		for s1 := Hash(0); s1 < domainN; s1++ {
			for s2 := Hash(0); s2 < domainN; s2++ {
				err := VerifyNonExistence(0, []Record{rec(h, s1), rec(h, s2)})
				if s1 == s2 {
					if errors.Is(err, ErrConflictSuccessor) {
						t.Fatalf("h=%d s=%d: 相同后继不应报冲突", h, s1)
					}
				} else if !errors.Is(err, ErrConflictSuccessor) {
					t.Fatalf("h=%d s1=%d s2=%d: 应报冲突后继，得到 %v", h, s1, s2, err)
				}
			}
		}
	}
}

// TestCrossZoneSplicing 验证不能拼接跨区证据：即使每份记录各自都能
// 证明目标不存在，只要版本或参数摘要不同就必须拒绝。
func TestCrossZoneSplicing(t *testing.T) {
	a := rec(1, 5) // 覆盖 target=3
	b := rec(1, 5)
	b.ZoneVersion = 8
	if err := VerifyNonExistence(3, []Record{a, b}); !errors.Is(err, ErrMixedZoneVersion) {
		t.Fatalf("跨版本拼接应拒绝，得到 %v", err)
	}

	c := rec(1, 5)
	c.Params = testParamsB
	if err := VerifyNonExistence(3, []Record{a, c}); !errors.Is(err, ErrMixedParams) {
		t.Fatalf("跨参数拼接应拒绝，得到 %v", err)
	}

	d := rec(1, 5)
	if err := VerifyNonExistence(3, []Record{a, d}); err != nil {
		t.Fatalf("同版本同参数应可组合，得到 %v", err)
	}
}

// ringRecords 为有限域上的节点集合生成正确的后继记录。
func ringRecords(nodes []Hash) []Record {
	records := make([]Record, 0, len(nodes))
	for i, h := range nodes {
		succ := nodes[(i+1)%len(nodes)]
		records = append(records, rec(h, succ))
	}
	return records
}

// TestRingsExhaustive 穷举有限域上的全部节点子集，按真实环模型生成
// 记录，验证：目标不在集合中当且仅当证明成立。
func TestRingsExhaustive(t *testing.T) {
	const n = 14
	for mask := 1; mask < 1<<n; mask++ {
		var nodes []Hash
		inSet := make(map[Hash]bool)
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				nodes = append(nodes, Hash(i))
				inSet[Hash(i)] = true
			}
		}
		records := ringRecords(nodes)
		for target := Hash(0); target < n; target++ {
			err := VerifyNonExistence(target, records)
			if inSet[target] {
				if !errors.Is(err, ErrNotProven) {
					t.Fatalf("mask=%b target=%d: 存在的节点不应被证明不存在，得到 %v", mask, target, err)
				}
			} else if err != nil {
				t.Fatalf("mask=%b target=%d: 缺失的名字应被证明不存在，得到 %v", mask, target, err)
			}
		}
	}
}

// TestNoEvidence 验证空证据集。
func TestNoEvidence(t *testing.T) {
	if err := VerifyNonExistence(0, nil); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("空证据集应报 ErrNoEvidence，得到 %v", err)
	}
}
