package commit

// Quote 只报价：返回严格最小总读费用与同费用下的最少读次数，不产出动作序列，
// 也不接触盘面。无解时返回 *InfeasibleError。
func Quote(inst *Instance) (readCost int64, reads int, err error) {
	e, err := newEngine(inst)
	if err != nil {
		return 0, 0, err
	}
	goal, _, err := e.search()
	if err != nil {
		return 0, 0, err
	}
	return goal.label[0], int(goal.label[1]), nil
}
