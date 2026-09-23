package featureneg

// minimizeCore reduces a required-feature set to a minimal subset that
// is still infeasible on its own (deletion-based minimization). The
// result is the "minimal conflicting condition subset" reported to the
// caller: removing any element from it makes the set feasible again.
func minimizeCore(ctx *negCtx, required []string) []string {
	core := append([]string(nil), required...)
	i := 0
	for i < len(core) {
		trial := make([]string, 0, len(core)-1)
		trial = append(trial, core[:i]...)
		trial = append(trial, core[i+1:]...)
		if !ctx.feasibleRequired(trial) {
			core = trial // still infeasible without core[i]: drop it
		} else {
			i++
		}
	}
	return core
}
