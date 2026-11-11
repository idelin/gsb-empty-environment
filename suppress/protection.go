package suppress

// dirCell 是交错环上的一个单元；plus 表示沿所选方向该单元取值加一，
// 否则减一。
type dirCell struct {
	cell Cell
	plus bool
}

// alternatingCycles 枚举二部图中经过 target、且边全部属于抑制集合 H 的
// 简单交错环。每个返回环以 target 的“加一”边开头，环上加减号交替。
// 4×4 以内环数量很少，直接深度优先枚举。
func alternatingCycles(hid [][]bool, target Cell) [][]dirCell {
	r, c := len(hid), len(hid[0])
	seen := make(map[string]bool)
	var cycles [][]dirCell
	emit := func(cells []Cell) {
		// 至少四个单元才是真正的二部图环；二边“环”只会取消防移。
		if len(cells) < 4 {
			return
		}
		// 同一无向环可能以不同遍历顺序被枚举；用环上的无向边集合做键
		// 去重，两个转移方向由调用方分别测试。
		mark := make([]bool, r*c)
		for _, cell := range cells {
			mark[cell.Row*c+cell.Col] = true
		}
		key := string(booleansToBytes(mark))
		if seen[key] {
			return
		}
		seen[key] = true
		cycles = append(cycles, orientCycle(cells))
	}

	// 从 target 边出发，沿二部图交替经过“内部行、内部列”，最后由
	// target.Row 上的边回到 target.Col。内部行、列均不得重复，target.Row
	// 与 target.Col 只允许在闭包边重新出现。
	usedRows := make([]bool, r)
	usedCols := make([]bool, c)
	rows := []int{target.Row}
	cols := []int{target.Col}

	var dfs func(row, col int)
	dfs = func(row, col int) {
		for nextRow := 0; nextRow < r; nextRow++ {
			if !hid[nextRow][col] || usedRows[nextRow] {
				continue
			}
			usedRows[nextRow] = true
			rows = append(rows, nextRow)
			for nextCol := 0; nextCol < c; nextCol++ {
				if !hid[nextRow][nextCol] || usedCols[nextCol] {
					continue
				}
				if nextRow == target.Row && nextCol == target.Col {
					emit(pathAsCells(rows, cols))
					continue
				}
				if nextRow == target.Row || nextCol == target.Col {
					continue
				}
				usedCols[nextCol] = true
				cols = append(cols, nextCol)
				dfs(nextRow, nextCol)
				cols = cols[:len(cols)-1]
				usedCols[nextCol] = false
			}
			rows = rows[:len(rows)-1]
			usedRows[nextRow] = false
		}
	}
	dfs(target.Row, target.Col)
	return cycles
}

// pathAsCells 把并行的行、列节点序列展平成环上的单元序列：
// [(r0,c0),(r1,c0),(r1,c1),...,(rk,c{k-1}),(r0,ck)]。
func pathAsCells(rows, cols []int) []Cell {
	cells := make([]Cell, 0, 2*len(cols))
	cells = append(cells, Cell{rows[0], cols[0]})
	for k := 1; k < len(cols); k++ {
		cells = append(cells, Cell{rows[k], cols[k-1]})
		cells = append(cells, Cell{rows[k], cols[k]})
	}
	cells = append(cells, Cell{rows[0], cols[len(cols)-1]})
	return cells
}

// orientCycle 把无向的环单元序列（以 target 开头）标成交替加减方向，
// target 固定为加一。
func orientCycle(cells []Cell) []dirCell {
	cycle := make([]dirCell, len(cells))
	for i, cell := range cells {
		cycle[i] = dirCell{cell: cell, plus: i%2 == 0}
	}
	return cycle
}

func booleansToBytes(values []bool) []byte {
	out := make([]byte, len(values))
	for i, v := range values {
		if v {
			out[i] = 1
		}
	}
	return out
}

// protectedWithWitnesses 判定 target 在抑制集合 H 下是否存在两个可行值。
// 对每个经过 target 的交错环测试两个转移方向。任一方向的单位转移可行，
// 就同时得到两张完整可行表：一张满足原始边界（基表），另一张沿环偏移一
// 个单位；二者在 H 之外完全相同，且 target 取值相差一。
func protectedWithWitnesses(lower, upper [][]int, rowSums, colSums []int, hid [][]bool, target Cell) ([][]int, [][]int, bool) {
	cycles := alternatingCycles(hid, target)
	for _, cycle := range cycles {
		for _, oriented := range [][]dirCell{cycle, reverseCycle(cycle)} {
			lo, up, okBounds := shiftedBounds(lower, upper, oriented)
			if !okBounds {
				continue
			}
			shifted, ok := feasibleTable(lo, up, rowSums, colSums)
			if !ok {
				continue
			}
			base, ok := shiftTable(shifted, lower, upper, reverseCycle(oriented))
			if !ok {
				continue
			}
			if targetValue(oriented, target) > 0 {
				return base, shifted, true
			}
			return shifted, base, true
		}
	}
	return nil, nil, false
}

// shiftTable 在一张表上沿环方向做一次单位转移，并核对结果仍满足逐单元
// 边界。对“收紧边界可行解”反向转移即可得到原始边界下的基表，二者仅在
// 环上相差一个单位。
func shiftTable(base, lower, upper [][]int, cycle []dirCell) ([][]int, bool) {
	r := len(base)
	out := make([][]int, r)
	for i := range base {
		out[i] = append([]int(nil), base[i]...)
	}
	for _, step := range cycle {
		i, j := step.cell.Row, step.cell.Col
		if step.plus {
			out[i][j]++
			if out[i][j] > upper[i][j] {
				return nil, false
			}
		} else {
			out[i][j]--
			if out[i][j] < lower[i][j] {
				return nil, false
			}
		}
	}
	return out, true
}

// shiftedBounds 沿环方向把每个单元的边界收紧一个单位：加一单元抬升下界，
// 减一单元压低下界。收紧后的边界若自身非法（低于原下界或高于原上界），
// 说明该方向单位转移必然越界，返回 ok=false。
func shiftedBounds(lower, upper [][]int, cycle []dirCell) ([][]int, [][]int, bool) {
	r := len(lower)
	lo := make([][]int, r)
	up := make([][]int, r)
	for i := 0; i < r; i++ {
		lo[i] = append([]int(nil), lower[i]...)
		up[i] = append([]int(nil), upper[i]...)
	}
	for _, step := range cycle {
		i, j := step.cell.Row, step.cell.Col
		if step.plus {
			lo[i][j]++
			if lo[i][j] > upper[i][j] {
				return nil, nil, false
			}
		} else {
			up[i][j]--
			if up[i][j] < lower[i][j] {
				return nil, nil, false
			}
		}
	}
	return lo, up, true
}

func reverseCycle(cycle []dirCell) []dirCell {
	reversed := make([]dirCell, len(cycle))
	for i, step := range cycle {
		reversed[i] = dirCell{cell: step.cell, plus: !step.plus}
	}
	return reversed
}

// targetValue 返回 target 在该方向转移中的增量（+1 或 -1）。
func targetValue(cycle []dirCell, target Cell) int {
	for _, step := range cycle {
		if step.cell == target {
			if step.plus {
				return 1
			}
			return -1
		}
	}
	return 0
}

// buildHidden 把敏感单元与额外抑制单元合并为布尔掩码。
func buildHidden(r, c int, sensitive []Cell, extra []Cell) [][]bool {
	hid := make([][]bool, r)
	for i := range hid {
		hid[i] = make([]bool, c)
	}
	for _, cell := range sensitive {
		hid[cell.Row][cell.Col] = true
	}
	for _, cell := range extra {
		hid[cell.Row][cell.Col] = true
	}
	return hid
}

// isProtected 仅做可行性判定，不重建见证，供子集搜索反复调用。
func isProtected(lower, upper [][]int, rowSums, colSums []int, hid [][]bool, target Cell) bool {
	_, _, ok := protectedWithWitnesses(lower, upper, rowSums, colSums, hid, target)
	return ok
}
