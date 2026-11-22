package recovery

import "fmt"

// Certificate 是判定结果的独立证书。信念以 BeliefKey 规范键标识。
type Certificate struct {
	// Feasible 与判定结果一致。
	Feasible bool
	// Bound 是初始信念的最坏额外帧数上界(可行时)。
	Bound int
	// Values 给出每个必胜信念的最坏额外帧数取值(可行时)。
	Values map[string]int
	// Choice 给出每个非目标必胜信念的选定动作(可行时)。
	Choice map[string]Action
	// Trap 是对抗陷阱信念集(不可行时):环境可永远把恢复过程
	// 困在 Trap 内且永不完成。
	Trap []string
}

// ActionFor 返回证书为给定信念选定的动作。
func (c *Certificate) ActionFor(states ...State) (Action, bool) {
	a, ok := c.Choice[BeliefKey(states...)]
	return a, ok
}

// Verify 独立校验证书,不复用 Solve 的搜索结论。
//
// 可行证书:验证者自行枚举信念图并计算布尔必胜域,然后只做局部检查——
// Values 的定义域必须恰为必胜域;目标信念取值为 0;每个非目标信念的
// 取值等于选定动作的最坏分支代价,且不超过任何其他全域内动作的代价。
// 由于转移成本 >= 1,该方程组的有限不动点唯一,故取值即博弈真值,
// Bound 同时是上界与下界证书。
//
// 不可行证书:验证者检查 Trap 含初始信念、不含目标信念,且每个陷阱
// 信念的每个有效动作都有至少一个观察分支留在陷阱内。于是对任意策略,
// 环境都能使过程永远停留在 Trap 中,恢复无法完成。
func Verify(m *Model, c *Certificate) error {
	an, err := analyze(m)
	if err != nil {
		return err
	}
	if !c.Feasible {
		if len(c.Trap) == 0 {
			return fmt.Errorf("recovery: 不可行证书缺少对抗陷阱")
		}
		trap := map[string]bool{}
		for _, k := range c.Trap {
			trap[k] = true
		}
		if !trap[an.start] {
			return fmt.Errorf("recovery: 陷阱不含初始信念 %q", an.start)
		}
		for _, k := range c.Trap {
			n, ok := an.nodes[k]
			if !ok {
				return fmt.Errorf("recovery: 陷阱含不可达信念 %q", k)
			}
			if n.goal {
				return fmt.Errorf("recovery: 陷阱含已完成信念 %q", k)
			}
			for a, outs := range n.acts {
				stays := false
				for _, o := range outs {
					if trap[o.sub] {
						stays = true
						break
					}
				}
				if !stays {
					return fmt.Errorf("recovery: 信念 %q 动作 %d 的所有分支均离开陷阱", k, a)
				}
			}
		}
		return nil
	}
	v0, ok := c.Values[an.start]
	if !ok {
		return fmt.Errorf("recovery: 证书缺少初始信念 %q 的取值", an.start)
	}
	if c.Bound != v0 {
		return fmt.Errorf("recovery: 证书上界 %d 与初始信念取值 %d 不符", c.Bound, v0)
	}
	if len(c.Values) != len(an.win) {
		return fmt.Errorf("recovery: 证书取值域大小 %d 与必胜域大小 %d 不符", len(c.Values), len(an.win))
	}
	for k := range an.win {
		if _, ok := c.Values[k]; !ok {
			return fmt.Errorf("recovery: 证书缺少必胜信念 %q 的取值", k)
		}
	}
	for k, v := range c.Values {
		n := an.nodes[k]
		if n.goal {
			if v != 0 {
				return fmt.Errorf("recovery: 已完成信念 %q 取值 %d 非零", k, v)
			}
			continue
		}
		a, ok := c.Choice[k]
		if !ok {
			return fmt.Errorf("recovery: 信念 %q 缺少选定动作", k)
		}
		outs, ok := n.acts[a]
		if !ok {
			return fmt.Errorf("recovery: 信念 %q 的选定动作 %d 无效", k, a)
		}
		worst := 0
		for _, o := range outs {
			ov, ok := c.Values[o.sub]
			if !ok {
				return fmt.Errorf("recovery: 信念 %q 的分支后继 %q 缺少取值", k, o.sub)
			}
			if c := o.cost + ov; c > worst {
				worst = c
			}
		}
		if worst != v {
			return fmt.Errorf("recovery: 信念 %q 取值 %d 与选定动作最坏代价 %d 不符", k, v, worst)
		}
		for a2, outs2 := range n.acts {
			if a2 == a {
				continue
			}
			w2 := 0
			inDomain := true
			for _, o := range outs2 {
				ov, ok := c.Values[o.sub]
				if !ok {
					inDomain = false
					break
				}
				if c := o.cost + ov; c > w2 {
					w2 = c
				}
			}
			if inDomain && w2 < v {
				return fmt.Errorf("recovery: 信念 %q 存在更优动作 %d(代价 %d < %d)", k, a2, w2, v)
			}
		}
	}
	return nil
}
