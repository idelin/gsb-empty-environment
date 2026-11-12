package netsec

import "math/rand/v2"

// Source 提供一次有界均匀整数抽取，返回 [0, n) 中的整数，n 必须为正数。
// 实现需自行保证并发安全；测试可注入确定性的脚本化实现。
type Source interface {
	Intn(n int) int
}

// defaultSource 包装 math/rand/v2 的顶层函数。顶层函数由运行时加锁，
// 可被多个 goroutine 安全并发使用。
type defaultSource struct{}

func (defaultSource) Intn(n int) int { return rand.IntN(n) }

// drawMatrix 按矩阵 matrix 的第 from 行抽取下一个符号。
func drawMatrix(matrix Matrix, from int, r Source) (int, error) {
	if from < 0 || from >= len(matrix.Row) {
		return 0, ErrSymbolOutOfRange
	}
	row, err := resolveRow(matrix.Row[from])
	if err != nil {
		return 0, err
	}
	return row.draw(r), nil
}
