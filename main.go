package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
)

// 每轮随机增删边（含重复边），输出变化的可达点对，
// 并用全图 BFS 重算结果核对增量索引。
func main() {
	seed := flag.Int64("seed", 1, "随机种子")
	nodes := flag.Int("nodes", 12, "节点数")
	rounds := flag.Int("rounds", 200, "操作轮数")
	verbose := flag.Bool("v", true, "打印每轮变化的点对")
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))
	g := NewGraph()
	idx := NewReachIndex(g)

	for round := 1; round <= *rounds; round++ {
		u := rng.Intn(*nodes)
		v := rng.Intn(*nodes)
		var changes []Change
		var op string
		// 60% 添加（允许重复边），40% 删除
		if rng.Intn(100) < 60 {
			changes = idx.Add(u, v)
			op = fmt.Sprintf("ADD    %d -> %d (count=%d)", u, v, g.Count(u, v))
		} else {
			changes = idx.Remove(u, v)
			op = fmt.Sprintf("REMOVE %d -> %d (count=%d)", u, v, g.Count(u, v))
		}

		if *verbose && len(changes) > 0 {
			sort.Slice(changes, func(i, j int) bool {
				if changes[i].Pair != changes[j].Pair {
					if changes[i].Pair.From != changes[j].Pair.From {
						return changes[i].Pair.From < changes[j].Pair.From
					}
					return changes[i].Pair.To < changes[j].Pair.To
				}
				return changes[i].Added && !changes[j].Added
			})
			fmt.Printf("round %3d  %s\n", round, op)
			for _, c := range changes {
				mark := "+"
				if !c.Added {
					mark = "-"
				}
				fmt.Printf("    %s (%d, %d)\n", mark, c.Pair.From, c.Pair.To)
			}
		}

		if diff := idx.VerifyAgainst(g); len(diff) > 0 {
			fmt.Printf("round %d  %s\nMISMATCH vs BFS oracle: %v\n", round, op, diff)
			os.Exit(1)
		}
	}
	fmt.Printf("OK: %d rounds, %d nodes, final reachable pairs = %d, all verified against full BFS\n",
		*rounds, *nodes, len(idx.Pairs()))
}
