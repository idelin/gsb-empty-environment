// Package join 实现双流按键窗口连接（interval join）。
//
// 语义：
//   - 左右两流事件按 Key 配对，仅当 |left.TS - right.TS| <= Window 时连接。
//   - 左右两侧各自维护独立水位（watermark），互不影响。
//   - 事件时间早于等于本侧水位的事件视为迟到：
//     仍在 AllowedLateness 更正期限内的，正常参与连接，产出的结果标记为
//     差量（Delta=true）；超出更正期限的不再处理，单独记入迟到报告。
//   - 状态清理只依赖对侧水位：左侧缓冲的事件只有在右侧水位推进到
//     “不可能再有能与其匹配的右事件”之后才会被删除，反之亦然。
//     因此一侧水位停滞时，另一侧水位再高也不会误删本侧可匹配数据。
package join

import "fmt"

// Side 标识事件所属的流。
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "left"
	}
	return "right"
}

// Event 是一条输入事件，TS 为事件时间（毫秒）。
type Event struct {
	Key   string
	TS    int64
	Value interface{}
}

// Pair 是一对连接结果。Delta 为 true 表示该结果由迟到但在更正期限内的
// 事件触发，属于对先前输出的追加差量（更正）。
type Pair struct {
	Left  Event
	Right Event
	Delta bool
}

// LateReport 记录一条超出更正期限、被丢弃的迟到事件。
type LateReport struct {
	Side  Side
	Event Event
	// Watermark 为该事件到达时本侧的水位。
	Watermark int64
}

func (r LateReport) String() string {
	return fmt.Sprintf("late %s event dropped: key=%q ts=%d watermark=%d",
		r.Side, r.Event.Key, r.Event.TS, r.Watermark)
}

type keyState struct {
	left  []Event
	right []Event
}

// Joiner 是双流窗口连接器。零值不可用，请使用 New。
type Joiner struct {
	window          int64
	allowedLateness int64

	wm    [2]int64 // 左右两侧各自的水位
	wmSet [2]bool

	state map[string]*keyState

	pairs []Pair
	late  []LateReport
}

// New 创建连接器。window 为允许的最大事件时间差（含边界），
// allowedLateness 为迟到事件仍可被接受并产生差量的更正期限。
func New(window, allowedLateness int64) *Joiner {
	if window < 0 || allowedLateness < 0 {
		panic("join: window and allowedLateness must be >= 0")
	}
	return &Joiner{
		window:          window,
		allowedLateness: allowedLateness,
		state:           make(map[string]*keyState),
	}
}

// Watermark 返回指定侧当前水位；尚未推进过时 ok 为 false。
func (j *Joiner) Watermark(side Side) (wm int64, ok bool) {
	return j.wm[side], j.wmSet[side]
}

// AdvanceWatermark 推进指定侧水位。水位单调递增，小于等于当前值的
// 推进请求被忽略（返回 false）。推进后按对侧水位清理过期状态。
func (j *Joiner) AdvanceWatermark(side Side, ts int64) bool {
	if j.wmSet[side] && ts <= j.wm[side] {
		return false
	}
	j.wm[side] = ts
	j.wmSet[side] = true
	j.cleanup()
	return true
}

// Add 摄入一条事件，返回本次新产生的连接结果（可能为空）。
// 迟到但在更正期限内的事件正常参与连接，结果标记 Delta=true；
// 超出更正期限的事件被丢弃并记入 LateReports，返回 nil。
func (j *Joiner) Add(side Side, ev Event) []Pair {
	delta := false
	if j.wmSet[side] && ev.TS <= j.wm[side] {
		// 迟到事件：ev.TS > wm - allowedLateness 时仍在更正期限内。
		if ev.TS > j.wm[side]-j.allowedLateness {
			delta = true
		} else {
			j.late = append(j.late, LateReport{Side: side, Event: ev, Watermark: j.wm[side]})
			return nil
		}
	}

	ks := j.state[ev.Key]
	if ks == nil {
		ks = &keyState{}
		j.state[ev.Key] = ks
	}

	var emitted []Pair
	if side == Left {
		for _, r := range ks.right {
			if withinWindow(ev.TS, r.TS, j.window) {
				emitted = append(emitted, Pair{Left: ev, Right: r, Delta: delta})
			}
		}
		ks.left = append(ks.left, ev)
	} else {
		for _, l := range ks.left {
			if withinWindow(l.TS, ev.TS, j.window) {
				emitted = append(emitted, Pair{Left: l, Right: ev, Delta: delta})
			}
		}
		ks.right = append(ks.right, ev)
	}

	j.pairs = append(j.pairs, emitted...)
	j.cleanup()
	return emitted
}

// Pairs 返回迄今为止产生的全部连接结果（含差量）。
func (j *Joiner) Pairs() []Pair { return j.pairs }

// LateReports 返回迄今为止被丢弃的迟到事件报告。
func (j *Joiner) LateReports() []LateReport { return j.late }

// Buffered 返回指定 key 两侧当前仍缓冲的事件数，用于观察状态清理。
func (j *Joiner) Buffered(key string) (left, right int) {
	if ks := j.state[key]; ks != nil {
		return len(ks.left), len(ks.right)
	}
	return 0, 0
}

func withinWindow(a, b, window int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= window
}

// cleanup 依据对侧水位清理不可能再匹配的状态。
//
// 左事件 ts 未来仍可能匹配的右事件，事件时间最小为
// rightWM - allowedLateness（更正期限内仍允许到达的迟到右事件）。
// 因此当 ts + window < rightWM - allowedLateness 时，该左事件
// 不可能再被任何未来的右事件匹配，可以安全删除。右侧对称。
// 注意：清理左状态只看右侧水位，清理右状态只看左侧水位，
// 一侧停滞不会导致另一侧状态被提前删除。
func (j *Joiner) cleanup() {
	for key, ks := range j.state {
		if j.wmSet[Right] {
			horizon := j.wm[Right] - j.allowedLateness
			ks.left = retain(ks.left, func(ts int64) bool {
				return ts+j.window >= horizon
			})
		}
		if j.wmSet[Left] {
			horizon := j.wm[Left] - j.allowedLateness
			ks.right = retain(ks.right, func(ts int64) bool {
				return ts+j.window >= horizon
			})
		}
		if len(ks.left) == 0 && len(ks.right) == 0 {
			delete(j.state, key)
		}
	}
}

func retain(evs []Event, keep func(ts int64) bool) []Event {
	out := evs[:0]
	for _, ev := range evs {
		if keep(ev.TS) {
			out = append(out, ev)
		}
	}
	return out
}
