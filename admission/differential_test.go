package admission

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

// TestDifferentialAgainstReference 在随机生成的更新 DAG 上对照两条
// 独立实现路径：区间引擎 Build/Admit 与逐页参考 ReferenceAdmit。
// 两者代码路径完全不同，结论必须逐页一致。
func TestDifferentialAgainstReference(t *testing.T) {
	for _, seed := range []int64{20261121, 42, 7, 99, 1 << 20} {
		rng := rand.New(rand.NewSource(seed))
		domainPages := uint64(32 + rng.Intn(96))
		for iter := 0; iter < 100; iter++ {
			in := randomAdmissionInput(rng, domainPages)
			got := runAdmit(t, in)

			refDecisions, err := ReferenceAdmit(in)
			if err != nil {
				t.Fatalf("seed %d iter %d ReferenceAdmit: %v",
					seed, iter, err)
			}
			ex, fb, un := RangesByVerdict(refDecisions)
			want := map[Verdict][]Range{
				VerdictExecutable: ex,
				VerdictForbidden:  fb,
				VerdictUnknown:    un,
			}
			for v := Verdict(0); v <= VerdictForbidden; v++ {
				if len(want[v]) == 0 {
					want[v] = nil
				}
				if len(got[v]) == 0 {
					got[v] = nil
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d iter %d mismatch\n got=%v\nwant=%v\nreceipts=%v\nheads=%v",
					seed, iter, got, want, in.Receipts, in.Heads)
			}
		}
	}
}

func randomAdmissionInput(rng *rand.Rand, pages uint64) AdmissionInput {
	cp := Checkpoint{
		Root:        root("R0"),
		Shards:      []Range{{0, MaxPage}},
		Permissions: fullPerm(PermExec),
		Base: []BaseContent{{
			Pages:  Range{0, pages - 1},
			Digest: digest("base"),
			Trust:  TrustTrusted,
		}},
	}
	pubs := []string{"vendor", "vendor", "vendor", "rogue", "bad"}
	roots := []string{"R0"}
	var receipts []Receipt
	nextID := 1

	steps := 4 + rng.Intn(8)
	for step := 0; step < steps; step++ {
		old := roots[rng.Intn(len(roots))]
		newName := rootName(nextID)
		nextID++
		start := uint64(rng.Intn(int(pages)))
		end := start + uint64(rng.Intn(int(pages-start)))
		pagesRange := Range{Start: start, End: end}
		pub := pubs[rng.Intn(len(pubs))]

		var rcpt Receipt
		if rng.Intn(2) == 0 {
			rcpt = Receipt{
				Publisher: pub,
				OldRoot:   root(old),
				NewRoot:   root(newName),
				Pages:     pagesRange,
				Kind:      KindContent,
				Content: []ContentEntry{{
					Pages:  pagesRange,
					Digest: digest(newName),
				}},
			}
		} else {
			rcpt = Receipt{
				Publisher:  pub,
				OldRoot:    root(old),
				NewRoot:    root(newName),
				Pages:      pagesRange,
				Kind:       KindPermission,
				RevokeExec: []Range{pagesRange},
			}
		}
		receipts = append(receipts, rcpt)
		roots = append(roots, newName)
	}

	// 随机挑选候选根：约四分之一使用默认叶根，其余包含内部根，
	// 显式模拟提交确认丢失（更新可能尚未持久化）。
	var heads [][]byte
	if rng.Intn(4) != 0 {
		headCount := 1 + rng.Intn(3)
		seen := map[string]bool{}
		for len(heads) < headCount {
			candidate := roots[rng.Intn(len(roots))]
			if seen[candidate] {
				continue
			}
			seen[candidate] = true
			heads = append(heads, root(candidate))
		}
	}

	return AdmissionInput{
		Checkpoint: cp,
		Receipts:   receipts,
		Heads:      heads,
		Request:    Range{0, pages - 1},
		Verifier: fakeVerifier{trusted: map[string]bool{
			"vendor": true,
		}},
	}
}

func rootName(id int) string {
	return "Rn" + strconv.Itoa(id)
}
