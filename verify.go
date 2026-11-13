package netsec

import "fmt"

// VerifyTrace 独立重放轨迹：从初态 u=x、v=p 出发，逐步核对轨迹中每个符号
// 对应的分支条件在当时状态下确实成立，并重新累积逆元。
// 任一符号非法、分支条件不符、步数超过 budget、轨迹结束时未收敛，
// 或重放得到的逆元与 inv 不一致，都会返回错误。
func VerifyTrace(x, p, inv uint64, trace string, budget int) error {
	if err := ValidateParams(x, p); err != nil {
		return err
	}
	if len(trace) > budget {
		return fmt.Errorf("netsec: trace length %d exceeds budget %d: %w", len(trace), budget, ErrBudget)
	}
	u, v := x, p
	ua, vb := uint64(1), uint64(0)
	for i := 0; i < len(trace); i++ {
		if u == 1 || v == 1 {
			return fmt.Errorf("netsec: trace continues after convergence at step %d", i)
		}
		switch c := trace[i]; c {
		case StepA:
			if u&1 != 0 {
				return fmt.Errorf("netsec: step %d claims A but u=%d is odd", i, u)
			}
			u >>= 1
			ua = halfMod(ua, p)
		case StepB:
			if u&1 == 0 {
				return fmt.Errorf("netsec: step %d claims B but branch A applies (u=%d even)", i, u)
			}
			if v&1 != 0 {
				return fmt.Errorf("netsec: step %d claims B but v=%d is odd", i, v)
			}
			v >>= 1
			vb = halfMod(vb, p)
		case StepC:
			if u&1 == 0 || v&1 == 0 {
				return fmt.Errorf("netsec: step %d claims C but a halving branch applies (u=%d, v=%d)", i, u, v)
			}
			if u <= v {
				return fmt.Errorf("netsec: step %d claims C but u=%d <= v=%d", i, u, v)
			}
			u = (u - v) >> 1
			ua = halfMod(subMod(ua, vb, p), p)
		case StepD:
			if u&1 == 0 || v&1 == 0 {
				return fmt.Errorf("netsec: step %d claims D but a halving branch applies (u=%d, v=%d)", i, u, v)
			}
			if u > v {
				return fmt.Errorf("netsec: step %d claims D but u=%d > v=%d", i, u, v)
			}
			v = (v - u) >> 1
			vb = halfMod(subMod(vb, ua, p), p)
		default:
			return fmt.Errorf("netsec: step %d has invalid symbol %q", i, c)
		}
	}
	if u != 1 && v != 1 {
		return fmt.Errorf("netsec: trace ends without convergence (u=%d, v=%d)", u, v)
	}
	want := ua
	if v == 1 {
		want = vb
	}
	if inv != want {
		return fmt.Errorf("netsec: inverse mismatch: callback returned %d, replay computed %d", inv, want)
	}
	if (inv*x)%p != 1 {
		return fmt.Errorf("netsec: %d is not the inverse of %d mod %d", inv, x, p)
	}
	return nil
}
