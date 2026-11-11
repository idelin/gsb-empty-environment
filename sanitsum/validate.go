package sanitsum

import "fmt"

func validate(g *FuncGraph) error {
	if g == nil {
		return fmt.Errorf("sanitsum: 函数图为空")
	}
	if len(g.Categories) == 0 {
		return fmt.Errorf("sanitsum: 函数 %q 未声明污染类别", g.Name)
	}
	if len(g.Categories) > 64 {
		return fmt.Errorf("sanitsum: 类别数 %d 超过 64 上限", len(g.Categories))
	}
	if g.Entry == "" || len(g.Blocks) == 0 {
		return fmt.Errorf("sanitsum: 函数 %q 缺少入口或基本块", g.Name)
	}
	seen := make(map[string]*Block, len(g.Blocks))
	for _, b := range g.Blocks {
		if b == nil || b.ID == "" {
			return fmt.Errorf("sanitsum: 函数 %q 存在空块或无名块", g.Name)
		}
		if _, dup := seen[b.ID]; dup {
			return fmt.Errorf("sanitsum: 块 ID %q 重复", b.ID)
		}
		seen[b.ID] = b
	}
	entry, ok := seen[g.Entry]
	if !ok {
		return fmt.Errorf("sanitsum: 入口块 %q 不存在", g.Entry)
	}
	for _, b := range g.Blocks {
		hasReturn := false
		for i, st := range b.Stmts {
			if _, isRet := st.(ReturnStmt); isRet {
				if i != len(b.Stmts)-1 {
					return fmt.Errorf("sanitsum: 块 %q 的 ReturnStmt 必须位于块尾", b.ID)
				}
				hasReturn = true
			}
			if err := validateStmt(g, st); err != nil {
				return err
			}
		}
		switch {
		case hasReturn && len(b.Succs) != 0:
			return fmt.Errorf("sanitsum: 返回块 %q 不能有后继", b.ID)
		case !hasReturn && len(b.Succs) == 0:
			return fmt.Errorf("sanitsum: 非返回块 %q 至少需要一个后继", b.ID)
		}
		for _, succ := range b.Succs {
			if _, ok := seen[succ]; !ok {
				return fmt.Errorf("sanitsum: 块 %q 的后继 %q 不存在", b.ID, succ)
			}
		}
	}
	if len(entry.Succs) == 0 {
		if _, isRet := entry.Stmts[len(entry.Stmts)-1].(ReturnStmt); !isRet {
			return fmt.Errorf("sanitsum: 入口块 %q 无后继且未返回", g.Entry)
		}
	}
	return nil
}

func validateStmt(g *FuncGraph, st Stmt) error {
	switch t := st.(type) {
	case SanitizeStmt:
		if t.Target.Var == "" {
			return fmt.Errorf("sanitsum: SanitizeStmt 缺少目标变量")
		}
		if _, err := catBit(g.Categories, t.Cat); err != nil {
			return err
		}
	case AssignStmt:
		if t.Dst.Var == "" {
			return fmt.Errorf("sanitsum: AssignStmt 缺少目标变量")
		}
		if err := validateOperand(g, t.RHS); err != nil {
			return err
		}
	case StoreStmt:
		if t.Dst.Var == "" || t.Dst.Field == "" {
			return fmt.Errorf("sanitsum: StoreStmt 目标必须是带字段的 Ref")
		}
		if err := validateOperand(g, t.RHS); err != nil {
			return err
		}
	case ReturnStmt:
		if t.Ret == "" {
			return fmt.Errorf("sanitsum: ReturnStmt 缺少返回值标签")
		}
	case BranchStmt:
		// 不透明分支占位，无需额外校验。
	default:
		return fmt.Errorf("sanitsum: 未知语句类型 %T", st)
	}
	return nil
}

func validateOperand(g *FuncGraph, o RefOrConst) error {
	if o.IsConst() {
		return nil
	}
	if o.Ref.Var == "" {
		return fmt.Errorf("sanitsum: 操作数缺少变量名")
	}
	return nil
}
