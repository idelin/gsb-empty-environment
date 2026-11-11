package sanitsum

import "fmt"

// topoOrder 用 DFS 三色标记求拓扑序，同时检测环路（输入要求无循环）。
func (a *analyzer) topoOrder() ([]string, error) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(a.blocks))
	var order []string
	var visit func(id string) error
	visit = func(id string) error {
		switch color[id] {
		case gray:
			return fmt.Errorf("sanitsum: 函数图存在循环，块 %q 同时在当前路径上", id)
		case black:
			return nil
		}
		color[id] = gray
		for _, succ := range a.blocks[id].Succs {
			if err := visit(succ); err != nil {
				return err
			}
		}
		color[id] = black
		order = append(order, id)
		return nil
	}
	if err := visit(a.g.Entry); err != nil {
		return nil, err
	}
	// 逆后序即拓扑序；不可达块在 Analyze 的入口状态检查中报错。
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}
