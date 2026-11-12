package netsec

import (
	mrand "math/rand"
	"reflect"
	"testing"
)

// generateRandomDAG 构造 n 个块的随机 DAG：边只从小编号指向大编号，
// 天然无环；再随机选择若干块作为分支头，保证每条分支可达一部分目标。
func generateRandomDAG(rng *mrand.Rand, n int) (*DAG, []BlockID) {
	blocks := make(map[BlockID]Block, n)
	for i := 0; i < n; i++ {
		id := BlockID(blockName(i))
		var parents []BlockID
		for j := 0; j < i; j++ {
			if rng.Float64() < 0.35 {
				parents = append(parents, BlockID(blockName(j)))
			}
		}
		if i > 0 && len(parents) == 0 {
			parents = append(parents, BlockID(blockName(rng.Intn(i))))
		}
		blocks[id] = Block{ID: id, Parents: parents}
	}

	branchCount := 1 + rng.Intn(3)
	branches := make([]Branch, 0, branchCount)
	heads := make(map[BlockID]struct{}, branchCount)
	for i := 0; i < branchCount; i++ {
		head := BlockID(blockName(rng.Intn(n)))
		if _, dup := heads[head]; dup {
			continue
		}
		heads[head] = struct{}{}
		branches = append(branches, Branch{Name: "branch-" + blockName(i), Head: head})
	}

	targetCount := 1 + rng.Intn(n)
	targets := rng.Perm(n)[:targetCount]
	targetIDs := make([]BlockID, 0, targetCount)
	for _, idx := range targets {
		targetIDs = append(targetIDs, BlockID(blockName(idx)))
	}
	return &DAG{Blocks: blocks, Branches: branches}, targetIDs
}

func blockName(i int) string {
	return "b" + string(rune('a'+(i/26))) + string(rune('a'+(i%26)))
}

// TestRandomizedIncrementalVsFullScan 在数百张随机小图上对照两条核验路径：
// 增量合并器按随机顺序分批吃证明，全遍历参考实现一次性核验同一批证明，
// 两者的未覆盖路径集合、完成判定与统计必须逐图一致。
func TestRandomizedIncrementalVsFullScan(t *testing.T) {
	rng := mrand.New(mrand.NewSource(20261112))
	signer := newTestSigner(t, "host")
	auth := newTestAuthenticator(signer)

	for round := 0; round < 200; round++ {
		n := 4 + rng.Intn(9)
		dag, targetIDs := generateRandomDAG(rng, n)
		targets := NewBlockSet(targetIDs...)

		verifier, err := NewVerifier(dag, targets, auth)
		if err != nil {
			t.Fatalf("round %d: new verifier: %v", round, err)
		}

		var allProofs []Proof
		for _, target := range targetIDs {
			for _, branch := range dag.Branches {
				// 约六成 (分支, 目标) 对给出分支作用域证明，制造部分覆盖。
				if rng.Float64() < 0.6 {
					proof := signer.sign(Proof{
						BlockID: target,
						Kind:    []ProofKind{KindDeletion, KindUndecryptable}[rng.Intn(2)],
						Scope:   branch.Name,
					})
					allProofs = append(allProofs, proof)
				}
			}
			// 约两成概率追加全局证明，覆盖该块的全部剩余分支引用。
			if rng.Float64() < 0.2 {
				allProofs = append(allProofs, signer.sign(Proof{
					BlockID: target,
					Kind:    KindDeletion,
					Scope:   GlobalScope,
				}))
			}
		}
		rng.Shuffle(len(allProofs), func(i, j int) { allProofs[i], allProofs[j] = allProofs[j], allProofs[i] })

		cut := 0
		if len(allProofs) > 0 {
			cut = rng.Intn(len(allProofs) + 1)
		}
		if err := verifier.AddBatch(allProofs[:cut]); err != nil {
			t.Fatalf("round %d: incremental batch: %v", round, err)
		}

		incremental := verifier.Verify()
		full, fullErr := FullScan(dag, targets, auth, allProofs[:cut])
		if fullErr != nil {
			t.Fatalf("round %d: full scan: %v", round, fullErr)
		}

		if got := pathStrings(incremental.UncoveredPaths); !reflect.DeepEqual(got, pathStrings(full.UncoveredPaths)) {
			t.Fatalf("round %d: uncovered mismatch\ninc=%v\nfull=%v", round, got, pathStrings(full.UncoveredPaths))
		}
		if incremental.Complete != full.Complete {
			t.Fatalf("round %d: complete mismatch inc=%v full=%v", round, incremental.Complete, full.Complete)
		}
		if incremental.Stats.ReachablePairs != full.Stats.ReachablePairs ||
			incremental.Stats.UncoveredPairs != full.Stats.UncoveredPairs {
			t.Fatalf("round %d: stats mismatch inc=%+v full=%+v", round, incremental.Stats, full.Stats)
		}
		if !incremental.CrossCheck.Matched {
			t.Fatalf("round %d: internal cross-check failed: %+v", round, incremental.CrossCheck)
		}
	}
}
