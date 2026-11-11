package tablegrid

import (
	"math/rand"
	"testing"
)

// TestRandomCoverage 确保随机生成确实触发了规范中的关键路径。
func TestRandomCoverage(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	seq := 0
	var conflicts, zeroSpans, colSpans, implicitGroups, rowGroups int
	for iter := 0; iter < 2000; iter++ {
		tnode := genTable(rng, &seq)
		g := Build(tnode)

		for _, rg := range g.Groups {
			if rg.Implicit {
				implicitGroups++
			}
			if rg.Tag != "tbody" || rg.Implicit {
				rowGroups++
			}
		}
		for _, cell := range g.Cells {
			if cell.ColSpan > 1 {
				colSpans++
			}
			if cell.RowSpan > 1 {
				zeroSpans++
			}
		}

		// 若某个槽位所在行的“理论顺序列”被推挤，即说明发生过冲突插列：
		// 用朴素方式重新放一遍并比较是否曾需要 grow，成本较高，这里改用
		// 列数大于每行最大 sourceCol+colspan 的代理判定。
		maxWidth := 0
		for _, cell := range g.Cells {
			if w := cell.SourceCol + cell.ColSpan; w > maxWidth {
				maxWidth = w
			}
		}
		if g.Cols > maxWidth {
			conflicts++
		}
	}

	t.Logf("conflicts=%d rowSpans=%d colSpans=%d implicitGroups=%d nonTbodyGroups=%d",
		conflicts, zeroSpans, colSpans, implicitGroups, rowGroups)
	if conflicts == 0 {
		t.Fatal("random suite never exercised overlap/rewalk path")
	}
	if zeroSpans == 0 || colSpans == 0 || implicitGroups == 0 {
		t.Fatal("random suite missed basic span/group paths")
	}
}

func TestStressRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rng := rand.New(rand.NewSource(99))
	seq := 0
	for iter := 0; iter < 10000; iter++ {
		assertNaiveMatch(t, genTable(rng, &seq), "stress")
	}
}
