package netsec

import "fmt"

// ActionKind 是脚本化插件在回调中可执行的动作类型。
type ActionKind int

const (
	// ActCallCurrent 对当前待释放句柄发起 Call。
	ActCallCurrent ActionKind = iota
	// ActMoveCurrent 把当前待释放句柄 MoveTo 到 Target 对象。
	ActMoveCurrent
	// ActClose 关闭 Target 对象。
	ActClose
)

// Action 是一个插件动作；Target 是对象名（ActMoveCurrent / ActClose 使用）。
type Action struct {
	Kind   ActionKind
	Target string
}

func (a Action) String() string {
	switch a.Kind {
	case ActCallCurrent:
		return "Call(当前句柄)"
	case ActMoveCurrent:
		return fmt.Sprintf("MoveTo(当前句柄 -> %s)", a.Target)
	case ActClose:
		return fmt.Sprintf("Close(%s)", a.Target)
	}
	return "?"
}

// ScriptPlugin 在首次 BeforeRelease 回调中按顺序执行脚本动作，
// 之后（包括嵌套关闭触发的回调）不再动作。每个动作的结果都被记录，
// 回调错误不会被吞掉。
type ScriptPlugin struct {
	host   *Host
	script []Action
	fired  bool

	Results       []error // 与 script 一一对应的动作结果
	OnClosedCalls int
}

// BeforeRelease 实现 Plugin。
func (p *ScriptPlugin) BeforeRelease(cb *Callback) {
	if p.fired {
		return
	}
	p.fired = true
	hd, _ := cb.Handle()
	for _, a := range p.script {
		var err error
		switch a.Kind {
		case ActCallCurrent:
			err = cb.Call(hd)
		case ActMoveCurrent:
			err = cb.MoveTo(hd, p.host.Object(a.Target))
		case ActClose:
			err = cb.Close(p.host.Object(a.Target))
		}
		p.Results = append(p.Results, err)
	}
}

// OnClosed 实现 Plugin。
func (p *ScriptPlugin) OnClosed(cb *Callback) { p.OnClosedCalls++ }

// Exploit 是搜索到的一段可触发违规的最短插件动作序列。
type Exploit struct {
	Script    []Action
	Violation Violation
	Trace     string // 完整重入轨迹（按深度缩进）
}

// DefaultAlphabet 返回默认动作字母表：A 为被关闭对象，B 为另一个 Open 对象。
func DefaultAlphabet() []Action {
	return []Action{
		{Kind: ActCallCurrent},
		{Kind: ActMoveCurrent, Target: "B"},
		{Kind: ActClose, Target: "A"},
		{Kind: ActClose, Target: "B"},
	}
}

// MoveCloseAlphabet 返回不含 Call 的字母表，用于专门搜索二次释放。
func MoveCloseAlphabet() []Action {
	return []Action{
		{Kind: ActMoveCurrent, Target: "B"},
		{Kind: ActClose, Target: "A"},
		{Kind: ActClose, Target: "B"},
	}
}

// FindShortestExploit 按脚本长度做广度优先搜索，返回最短的可触发
// “同一句柄二次 Release”或“进入 Closing 后成功 Call”的插件动作序列；
// 在 maxDepth 内未找到则返回 nil。
//
// 场景固定为：对象 A 持有 1 个句柄并被 Close，对象 B 为空且保持 Open；
// 脚本动作在第一次 BeforeRelease 回调中依次执行。
func FindShortestExploit(mode Mode, alphabet []Action, maxDepth int) *Exploit {
	for depth := 0; depth <= maxDepth; depth++ {
		for _, script := range sequences(alphabet, depth) {
			if exploit := simulate(mode, script); exploit != nil {
				return exploit
			}
		}
	}
	return nil
}

func simulate(mode Mode, script []Action) *Exploit {
	plugin := &ScriptPlugin{script: script}
	var h *Host
	if mode == ModePendingReview {
		h = NewPendingReview(nil, plugin)
	} else {
		h = New(nil, plugin)
	}
	plugin.host = h
	a := h.NewObject("A")
	h.NewObject("B")
	if _, err := h.AddHandle(a); err != nil {
		panic(err)
	}
	_ = h.Close(a)
	if vs := h.Violations(); len(vs) > 0 {
		return &Exploit{
			Script:    append([]Action(nil), script...),
			Violation: vs[0],
			Trace:     h.Trace(),
		}
	}
	return nil
}

func sequences(alphabet []Action, n int) [][]Action {
	if n == 0 {
		return [][]Action{{}}
	}
	var out [][]Action
	var rec func(prefix []Action, k int)
	rec = func(prefix []Action, k int) {
		if k == 0 {
			out = append(out, append([]Action(nil), prefix...))
			return
		}
		for _, a := range alphabet {
			rec(append(prefix, a), k-1)
		}
	}
	rec(nil, n)
	return out
}
