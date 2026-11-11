package netsec

import "errors"

// 评估器 B：与 pipeline.go 中的评估器 A 完全独立编写的显式状态机。
// 它每次只处理当前阶段输入上的一个位置，产生一条 Step 记录，
// 并为每个输出字节保留指向上一阶段输出位置的来源链 Origins。
// 每处理一个输入位置消耗一个预算单位；预算耗尽立即停止并返回 errBudgetExhausted。

var (
	errBudgetExhausted = errors.New("netsec: step budget exhausted")
	errDeclaredReject  = errors.New("netsec: input contains a dangerous delimiter")
)

// 逐步规则名，便于宿主审计每个变换位置。
const (
	ruleDeclaredAccept   = "declared-accept"
	ruleDeclaredReject   = "declared-reject"
	rulePercentEscape    = "percent-escape"
	rulePercentLiteral   = "percent-literal"
	ruleCopy             = "copy"
	ruleBackslashEscape  = "backslash-escape"
	ruleBackslashLiteral = "backslash-literal"
	ruleDeleteDrop       = "delete-drop"
	ruleDeleteKeep       = "delete-keep"
)

type stepper struct {
	danger byteSet
	del    byteSet
	m      map[byte]byte
	budget int64
	trace  []Step
}

// charge 在处理下一个位置前扣减预算；耗尽时返回错误。
func (s *stepper) charge() error {
	if s.budget <= 0 {
		return errBudgetExhausted
	}
	s.budget--
	return nil
}

func (s *stepper) emit(stage, pos int, rule string, emitted []byte, origins [][]int) {
	s.trace = append(s.trace, Step{
		Stage:   stage,
		Rule:    rule,
		Pos:     pos,
		Emitted: cloneBytes(emitted),
		Origins: cloneOrigins(origins),
	})
}

func cloneOrigins(o [][]int) [][]int {
	if len(o) == 0 {
		return nil
	}
	c := make([][]int, len(o))
	for i := range o {
		c[i] = append([]int(nil), o[i]...)
	}
	return c
}

// stageDeclaredB 独立复核声明检查：逐位置核对输入不含危险分隔符。
func (s *stepper) stageDeclaredB(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for p, b := range in {
		if err := s.charge(); err != nil {
			return nil, err
		}
		if s.danger.contains(b) {
			s.emit(stageDeclared, p, ruleDeclaredReject, nil, nil)
			return nil, errDeclaredReject
		}
		s.emit(stageDeclared, p, ruleDeclaredAccept, []byte{b}, [][]int{{p}})
		out = append(out, b)
	}
	return out, nil
}

// stagePercentB 逐步执行百分号解码，逐位置决定逃逸、字面量或复制。
func (s *stepper) stagePercentB(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for p := 0; p < len(in); {
		if err := s.charge(); err != nil {
			return nil, err
		}
		if in[p] == '%' && p+2 < len(in) && isHex(in[p+1]) && isHex(in[p+2]) {
			decoded := hexVal(in[p+1])<<4 | hexVal(in[p+2])
			s.emit(stagePercent, p, rulePercentEscape,
				[]byte{decoded}, [][]int{{p, p + 1, p + 2}})
			out = append(out, decoded)
			p += 3
			continue
		}
		if in[p] == '%' {
			s.emit(stagePercent, p, rulePercentLiteral,
				[]byte{in[p]}, [][]int{{p}})
			out = append(out, in[p])
			p++
			continue
		}
		s.emit(stagePercent, p, ruleCopy,
			[]byte{in[p]}, [][]int{{p}})
		out = append(out, in[p])
		p++
	}
	return out, nil
}

// stageBackslashB 逐步执行反斜线转义解码，独立维护消费位置。
func (s *stepper) stageBackslashB(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for p := 0; p < len(in); {
		if err := s.charge(); err != nil {
			return nil, err
		}
		if in[p] == '\\' && p+1 < len(in) {
			if v, ok := s.m[in[p+1]]; ok {
				s.emit(stageBackslash, p, ruleBackslashEscape,
					[]byte{v}, [][]int{{p, p + 1}})
				out = append(out, v)
				p += 2
				continue
			}
		}
		if in[p] == '\\' {
			s.emit(stageBackslash, p, ruleBackslashLiteral,
				[]byte{in[p]}, [][]int{{p}})
			out = append(out, in[p])
			p++
			continue
		}
		s.emit(stageBackslash, p, ruleCopy,
			[]byte{in[p]}, [][]int{{p}})
		out = append(out, in[p])
		p++
	}
	return out, nil
}

// stageDeleteB 逐步执行删除阶段，每个位置都有保留或删除的明确记录。
func (s *stepper) stageDeleteB(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for p, b := range in {
		if err := s.charge(); err != nil {
			return nil, err
		}
		if s.del.contains(b) {
			s.emit(stageDelete, p, ruleDeleteDrop, nil, nil)
			continue
		}
		s.emit(stageDelete, p, ruleDeleteKeep,
			[]byte{b}, [][]int{{p}})
		out = append(out, b)
	}
	return out, nil
}

// runPipelineB 用逐步状态机跑完整条管线，返回各阶段输出、逐步轨迹、剩余预算与错误。
// 声明检查不通过返回 errDeclaredReject；预算不足返回 errBudgetExhausted。
func runPipelineB(in []byte, c Config, danger, del byteSet, budget int64) (stages [stageCount][]byte, trace []Step, remaining int64, err error) {
	s := &stepper{
		danger: danger,
		del:    del,
		m:      c.BackslashMap,
		budget: budget,
		trace:  make([]Step, 0, len(in)*stageCount),
	}
	stages[stageDeclared], err = s.stageDeclaredB(in)
	if err != nil {
		return stages, s.trace, s.budget, err
	}
	stages[stagePercent], err = s.stagePercentB(stages[stageDeclared])
	if err != nil {
		return stages, s.trace, s.budget, err
	}
	stages[stageBackslash], err = s.stageBackslashB(stages[stagePercent])
	if err != nil {
		return stages, s.trace, s.budget, err
	}
	stages[stageDelete], err = s.stageDeleteB(stages[stageBackslash])
	if err != nil {
		return stages, s.trace, s.budget, err
	}
	return stages, s.trace, s.budget, nil
}

// crossCheck 逐阶段、逐字节比对两套独立实现的输出。
// 返回空串表示一致；否则返回首个分歧位置的描述。
func crossCheck(a, b [stageCount][]byte) string {
	for st := 0; st < stageCount; st++ {
		x, y := a[st], b[st]
		if len(x) != len(y) {
			return stageMismatch(st, "length", len(x), len(y))
		}
		for i := range x {
			if x[i] != y[i] {
				return stageMismatch(st, "byte", i, int(x[i])^int(y[i]))
			}
		}
	}
	return ""
}

func stageMismatch(stage int, kind string, x, y int) string {
	names := [...]string{"declared", "percent", "backslash", "delete"}
	return "evaluator mismatch at stage " + names[stage] + " " + kind +
		": A=" + itoa(x) + " B=" + itoa(y)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
