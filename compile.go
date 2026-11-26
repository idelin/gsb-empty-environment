package policycheck

import "fmt"

// CompilePolicy 把参考规则链编译为严格前向的规范判定 DAG。
//
// 编译器按全部原子谓词做有序分支；每个叶子直接使用该真值最小项
// 下的参考链决定，因此符号语义与参考链完全一致。它既可作为安全
// 优化基线，也可用于测试调用方自有优化器的证书和等价报告。
func CompilePolicy(policy *Policy) (*DAG, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	atoms := collectPolicyAtoms(policy)
	if len(atoms) >= 31 {
		return nil, fmt.Errorf("policycheck: cannot compile %d atoms into a bounded DAG", len(atoms))
	}

	dag := &DAG{Name: "canonical-policy-dag", Root: 0}
	values := make(map[string]bool, len(atoms))

	var build func(pos int) (int, error)
	build = func(pos int) (int, error) {
		idx := len(dag.Nodes)
		dag.Nodes = append(dag.Nodes, &DAGNode{})

		if pos == len(atoms) {
			dec, err := policy.EvaluateSymbolic(values)
			if err != nil {
				return 0, err
			}
			dag.Nodes[idx].ID = fmt.Sprintf("leaf-%d", idx)
			dag.Nodes[idx].Decision = &dec
			return idx, nil
		}

		atom := atoms[pos]
		key := atom.AtomKey()
		dag.Nodes[idx].ID = fmt.Sprintf("test-%s", shortDigest(key))
		dag.Nodes[idx].Test = atom

		values[key] = false
		falseRoot, err := build(pos + 1)
		if err != nil {
			return 0, err
		}
		values[key] = true
		trueRoot, err := build(pos + 1)
		if err != nil {
			return 0, err
		}

		// Child indices were allocated after idx, so both edges are forward.
		dag.Nodes[idx].False = Edge{To: falseRoot}
		dag.Nodes[idx].True = Edge{To: trueRoot}
		return idx, nil
	}

	root, err := build(0)
	if err != nil {
		return nil, err
	}
	dag.Root = root
	if err := dag.ValidateForward(); err != nil {
		return nil, err
	}
	return dag, nil
}

func collectPolicyAtoms(policy *Policy) []*Pred {
	var roots []*Pred
	for i := range policy.Rules {
		roots = append(roots, policy.Rules[i].When)
	}
	seen := make(map[string]*Pred)
	for _, root := range roots {
		for _, atom := range CollectAtoms(root) {
			seen[atom.AtomKey()] = atom
		}
	}
	keys := sortedKeys(seen)
	out := make([]*Pred, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func shortDigest(key string) string {
	const prefix = "sha256:"
	if len(key) > len(prefix)+12 && key[:len(prefix)] == prefix {
		return key[len(prefix) : len(prefix)+12]
	}
	if len(key) > 12 {
		return key[:12]
	}
	return key
}
