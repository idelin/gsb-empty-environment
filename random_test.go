package tablegrid

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// genTable 随机生成结构合法但跨度激进的表格，覆盖：
// 显式行组与隐式 tbody、th/td 混用、colspan、rowspan（含 0）、
// 行内单元格数不齐、重叠冲突与空槽位。
func genTable(rng *rand.Rand, seq *int) *testNode {
	var children []*testNode

	emitRow := func(groupTag string) *testNode {
		n := rng.Intn(5) + 1
		cells := make([]*testNode, 0, n)
		for i := 0; i < n; i++ {
			*seq = *seq + 1
			tag := "td"
			if groupTag == "thead" || rng.Intn(6) == 0 {
				tag = "th"
			}
			node := cellNode(tag, fmt.Sprintf("c%d", *seq))

			switch rng.Intn(10) {
			case 0, 1:
				node.with("colspan", fmt.Sprintf("%d", rng.Intn(3)+1))
			case 2:
				node.with("colspan", "0") // 非法：应回退为 1
			case 3:
				node.with("colspan", "banana")
			}

			switch rng.Intn(12) {
			case 0, 1, 2:
				node.with("rowspan", fmt.Sprintf("%d", rng.Intn(3)+1))
			case 3:
				node.with("rowspan", "0") // 延伸到行组末尾
			case 4:
				node.with("rowspan", "-5")
			}
			cells = append(cells, node)
		}
		return tr(cells...)
	}

	groupCount := rng.Intn(3) + 1
	for g := 0; g < groupCount; g++ {
		tag := []string{"thead", "tbody", "tfoot"}[rng.Intn(3)]
		// 偶尔放行组外的裸 tr，制造隐式 tbody。
		if rng.Intn(4) == 0 {
			children = append(children, emitRow(""))
			continue
		}
		rows := rng.Intn(4) + 1
		groupRows := make([]*testNode, 0, rows)
		for r := 0; r < rows; r++ {
			groupRows = append(groupRows, emitRow(tag))
		}
		children = append(children, elem(tag, groupRows...))
	}
	return table(children...)
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	seq := 0
	for iter := 0; iter < 2000; iter++ {
		tnode := genTable(rng, &seq)
		label := fmt.Sprintf("random#%d", iter)
		assertNaiveMatch(t, tnode, label)

		// 确定性：同一输入两次构建结果必须完全一致。
		g1 := Build(tnode)
		g2 := Build(tnode)
		assertGridEqual(t, g1, g2, label)

		// 行组区间必须互不重叠、按序覆盖全部行。
		assertGroupsCoverRows(t, g1, label)
	}
}

func assertGridEqual(t *testing.T, g1, g2 *Grid, label string) {
	t.Helper()
	if g1.Rows != g2.Rows || g1.Cols != g2.Cols {
		t.Fatalf("%s: size not deterministic: %dx%d vs %dx%d", label, g1.Rows, g1.Cols, g2.Rows, g2.Cols)
	}
	m1, m2 := gridMatrix(g1), gridMatrix(g2)
	for r := range m1 {
		for c := range m1[r] {
			if m1[r][c] != m2[r][c] {
				t.Fatalf("%s: content not deterministic at (%d,%d)", label, r, c)
			}
		}
	}
	if len(g1.Cells) != len(g2.Cells) {
		t.Fatalf("%s: cell count not deterministic", label)
	}
	for i := range g1.Cells {
		a, b := g1.Cells[i], g2.Cells[i]
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: cell %d not deterministic:\n %+v\n %+v", label, i, a, b)
		}
	}
}

func assertGroupsCoverRows(t *testing.T, g *Grid, label string) {
	t.Helper()
	if len(g.Groups) == 0 {
		return
	}
	next := 0
	for _, rg := range g.Groups {
		if rg.StartRow != next {
			t.Fatalf("%s: group gap/overlap at %+v (next=%d)", label, rg, next)
		}
		if rg.Rows <= 0 {
			t.Fatalf("%s: non-positive group rows %+v", label, rg)
		}
		next += rg.Rows
	}
	// 行组只统计真实 <tr>；末尾单元格的 rowspan 可能让网格长出额外空行。
	if next > g.Rows {
		t.Fatalf("%s: groups cover %d rows, grid only has %d", label, next, g.Rows)
	}
}

// TestRandomHeaderPaths 对带 thead 的随机表，校验列路径与表头槽位一致。
func TestRandomHeaderPaths(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	seq := 0
	for iter := 0; iter < 500; iter++ {
		tnode := genTable(rng, &seq)
		g := Build(tnode)
		hasHead := false
		for _, rg := range g.Groups {
			if rg.Tag == "thead" {
				hasHead = true
			}
		}
		if !hasHead {
			continue
		}
		for c, col := range g.Columns {
			// 路径中的每一段都能在该列的表头行槽位中按顺序找到。
			pi := 0
			for _, rg := range g.Groups {
				if rg.Tag != "thead" {
					continue
				}
				for r := rg.StartRow; r < rg.StartRow+rg.Rows; r++ {
					cell := g.CellAt(r, c)
					if cell == nil || !cell.IsHeader {
						continue
					}
					if pi < len(col.HeaderPath) && cell.Text == col.HeaderPath[pi] {
						pi++
					}
				}
			}
			if pi != len(col.HeaderPath) {
				t.Fatalf("iter %d col %d: header path %v not traceable in slots", iter, c, col.HeaderPath)
			}
		}
	}
}
