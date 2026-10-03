package scheduler

// Interval 表示半开区间 [From, To)。
type Interval struct {
	From int64
	To   int64
}

func (iv Interval) empty() bool { return iv.From >= iv.To }

// subtract 计算 base 减去 covers 的并集后剩余的空缺区间（按起点升序）。
func subtract(base Interval, covers []Interval) []Interval {
	var gaps []Interval
	cursor := base.From
	for _, c := range covers {
		if c.To <= cursor {
			continue
		}
		if c.From > cursor {
			end := c.From
			if end > base.To {
				end = base.To
			}
			gaps = append(gaps, Interval{From: cursor, To: end})
		}
		if c.To > cursor {
			cursor = c.To
		}
		if cursor >= base.To {
			return gaps
		}
	}
	if cursor < base.To {
		gaps = append(gaps, Interval{From: cursor, To: base.To})
	}
	return gaps
}
