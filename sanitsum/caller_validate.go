package sanitsum

import "fmt"

func validateCaller(g *CallerGraph) error {
	if g == nil {
		return fmt.Errorf("sanitsum: 调用方图为空")
	}
	if len(g.Categories) == 0 {
		return fmt.Errorf("sanitsum: 调用方 %q 未声明污染类别", g.Name)
	}
	if len(g.Categories) > 64 {
		return fmt.Errorf("sanitsum: 类别数 %d 超过 64 上限", len(g.Categories))
	}
	if g.Entry == "" || len(g.Blocks) == 0 {
		return fmt.Errorf("sanitsum: 调用方 %q 缺少入口或基本块", g.Name)
	}
	seen := make(map[string]*CallerBlock, len(g.Blocks))
	for _, b := range g.Blocks {
		if b == nil || b.ID == "" {
			return fmt.Errorf("sanitsum: 调用方存在空块或无名块")
		}
		if _, dup := seen[b.ID]; dup {
			return fmt.Errorf("sanitsum: 调用方块 ID %q 重复", b.ID)
		}
		seen[b.ID] = b
	}
	if _, ok := seen[g.Entry]; !ok {
		return fmt.Errorf("sanitsum: 调用方入口块 %q 不存在", g.Entry)
	}
	for _, b := range g.Blocks {
		calls := 0
		branched := make(map[int]struct{})
		for _, st := range b.Stmts {
			switch t := st.(type) {
			case TaintStmt:
				if t.Dst.Var == "" {
					return fmt.Errorf("sanitsum: TaintStmt 缺少目标变量")
				}
				if _, err := catBit(g.Categories, t.Cat); err != nil {
					return err
				}
			case CallStmt:
				calls++
			case BranchOnStmt:
				if t.CallIndex < 0 || t.CallIndex >= calls {
					return fmt.Errorf("sanitsum: 块 %q 的 BranchOnStmt 引用了不存在的调用序号", b.ID)
				}
				if _, dup := branched[t.CallIndex]; dup {
					return fmt.Errorf("sanitsum: 块 %q 对同一调用重复分支", b.ID)
				}
				branched[t.CallIndex] = struct{}{}
				if len(t.Edges) == 0 {
					return fmt.Errorf("sanitsum: 块 %q 的 BranchOnStmt 缺少边", b.ID)
				}
				if _, ok := t.Edges[t.Expect]; !ok {
					return fmt.Errorf("sanitsum: 块 %q 的 Expect %q 不在分支边中", b.ID, t.Expect)
				}
				for _, succ := range t.Edges {
					if _, ok := seen[succ]; !ok {
						return fmt.Errorf("sanitsum: 块 %q 分支后继 %q 不存在", b.ID, succ)
					}
				}
			case SinkStmt:
				if t.Target.Var == "" {
					return fmt.Errorf("sanitsum: SinkStmt 缺少目标变量")
				}
				if _, err := catBit(g.Categories, t.Cat); err != nil {
					return err
				}
			default:
				return fmt.Errorf("sanitsum: 未知调用方语句类型 %T", st)
			}
		}
		for _, succ := range b.Succs {
			if _, ok := seen[succ]; !ok {
				return fmt.Errorf("sanitsum: 块 %q 的后继 %q 不存在", b.ID, succ)
			}
		}
	}
	return nil
}

// topoOrder 对调用方图做三色 DFS 拓扑排序并检测环路。
func (a *callerAnalyzer) topoOrder() ([]string, error) {
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
			return fmt.Errorf("sanitsum: 调用方图存在循环，块 %q 同时在当前路径上", id)
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
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}
