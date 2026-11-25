package typeverifier

import (
	"errors"
	"fmt"
	"strings"
)

// EdgeStep 描述拒绝路径上的一条控制流边。
type EdgeStep struct {
	From      int
	To        int
	Exception bool
	Handler   int
}

// VerifyError 是首个拒绝点及其到达路径。
type VerifyError struct {
	PC     int
	Reason string
	Path   []EdgeStep
}

func (e *VerifyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "typeverifier: 指令 %d 校验失败: %s", e.PC, e.Reason)
	if len(e.Path) > 0 {
		b.WriteString("；拒绝路径: ")
		for i, s := range e.Path {
			if i > 0 {
				b.WriteString(" -> ")
			}
			if s.Exception {
				fmt.Fprintf(&b, "%d =[异常:%d]=> %d", s.From, s.Handler, s.To)
			} else {
				fmt.Fprintf(&b, "%d -> %d", s.From, s.To)
			}
		}
	}
	return b.String()
}

type engine struct {
	method    *Method
	hierarchy *Hierarchy
	throwable string

	frames     []*frame
	pred       []*EdgeStep
	worklist   []int
	inWorklist []bool
}

// Verify 对已解码指令数组做基于工作表不动点的类型验证，返回首个拒绝点及其路径；
// 通过时返回 nil。它不解析完整 class 文件，只消费 Method 中已解码的指令与异常表。
func Verify(h *Hierarchy, m *Method) error {
	if h == nil {
		return errors.New("typeverifier: 类层次为 nil")
	}
	if m == nil {
		return errors.New("typeverifier: 方法为 nil")
	}
	if len(m.Code) == 0 {
		return errors.New("typeverifier: 指令数组为空")
	}
	if m.MaxLocals < 0 || m.MaxStack < 0 {
		return errors.New("typeverifier: 槽位/栈容量不能为负")
	}
	throwable := m.Throwable
	if throwable == "" {
		throwable = DefaultThrowable
	}
	if !h.Has(throwable) {
		return fmt.Errorf("typeverifier: 异常根类 %s 不在类层次中", throwable)
	}

	e := &engine{
		method:     m,
		hierarchy:  h,
		throwable:  throwable,
		frames:     make([]*frame, len(m.Code)),
		pred:       make([]*EdgeStep, len(m.Code)),
		inWorklist: make([]bool, len(m.Code)),
	}
	if err := e.validateStructure(); err != nil {
		return err
	}

	e.frames[0] = newFrame(m.MaxLocals)
	e.enqueue(0)
	for len(e.worklist) > 0 {
		pc := e.worklist[0]
		e.worklist = e.worklist[1:]
		e.inWorklist[pc] = false
		if err := e.simulate(pc); err != nil {
			return err
		}
	}
	return nil
}

func (e *engine) enqueue(pc int) {
	if !e.inWorklist[pc] {
		e.inWorklist[pc] = true
		e.worklist = append(e.worklist, pc)
	}
}

func (e *engine) validPC(pc int) bool {
	return pc >= 0 && pc < len(e.method.Code)
}

func (e *engine) failStatic(pc int, reason string) error {
	return &VerifyError{PC: pc, Reason: reason}
}

func (e *engine) failAt(pc int, reason string) error {
	return &VerifyError{PC: pc, Reason: reason, Path: e.pathTo(pc)}
}

func (e *engine) failEdge(step EdgeStep, reason string) error {
	path := e.pathTo(step.From)
	path = append(path, step)
	return &VerifyError{PC: step.To, Reason: reason, Path: path}
}

// pathTo 沿记录的前驱边回溯从入口到 pc 的路径。
func (e *engine) pathTo(pc int) []EdgeStep {
	var reversed []EdgeStep
	seen := map[int]bool{}
	for pc != 0 && !seen[pc] {
		seen[pc] = true
		step := e.pred[pc]
		if step == nil {
			break
		}
		reversed = append(reversed, *step)
		pc = step.From
	}
	path := make([]EdgeStep, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return path
}
