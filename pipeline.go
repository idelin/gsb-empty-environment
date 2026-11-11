package netsec

// 管线固定四个阶段，顺序为：
//  0. 声明检查（输入是否直接含有危险分隔符）
//  1. 百分号解码（%XX，最多在阶段内解码一次，不递归）
//  2. 反斜线转义解码（按 BackslashMap 映射，未命中则保留反斜线）
//  3. 删除 Delete 集合中的字节
//
// 包内提供两套彼此独立的实现：
//   - 评估器 A（本文件）：面向每阶段的批量变换，风格直接；
//   - 评估器 B（stepper.go）：显式状态机，逐位置执行并记录来源链。
// 搜索与单次分析均用两者交叉核对，任一处不一致即判 VerificationFailed。

const (
	stageDeclared = iota
	stagePercent
	stageBackslash
	stageDelete
	stageCount
)

const (
	// MaxWitnessLen 是题目规定的输入长度上限。
	MaxWitnessLen = 12
	// DefaultBudget 是默认原子步骤预算（评估器 B 每处理一个输入位置计一步）。
	DefaultBudget = 1_000_000
)

// Status 是搜索结论。
type Status int

const (
	// Unknown 表示预算耗尽，无法在预算内给出结论。
	Unknown Status = iota
	// WitnessFound 表示找到了通过声明检查却重新产生危险分隔符的最短反例。
	WitnessFound
	// Safe 表示在允许的输入空间（长度 <= MaxLen）内穷举完毕，不存在反例。
	Safe
	// InvalidConfig 表示配置本身不合法。
	InvalidConfig
	// VerificationFailed 表示两套独立实现在某个变换位置上结论不一致。
	VerificationFailed
)

// String 返回状态的稳定文本表示。
func (s Status) String() string {
	switch s {
	case WitnessFound:
		return "witness_found"
	case Safe:
		return "safe"
	case InvalidConfig:
		return "invalid_config"
	case VerificationFailed:
		return "verification_failed"
	default:
		return "unknown"
	}
}

// Config 描述一条有限变换管线。
type Config struct {
	// Alphabet 是允许出现在输入中的字节集合（有限字节集合）。
	Alphabet []byte
	// Dangerous 是危险分隔符字节集合。
	Dangerous []byte
	// BackslashMap 是反斜线转义映射：'\\' + key -> value。
	BackslashMap map[byte]byte
	// Delete 是末阶段要删除的字节集合。
	Delete []byte
	// MaxLen 是输入长度上限，<=0 时取 MaxWitnessLen，超过 MaxWitnessLen 视为配置错误。
	MaxLen int
	// Budget 是逐步执行的原子步骤预算，<=0 时取 DefaultBudget。
	Budget int64
}

// Step 是评估器 B 对单个变换位置的逐步记录。
type Step struct {
	// Stage 是阶段编号（stageDeclared/stagePercent/...）。
	Stage int
	// Rule 是该位置应用的规则名。
	Rule string
	// Pos 是当前阶段输入中的位置。
	Pos int
	// Emitted 是该步输出的字节；声明检查阶段为空。
	Emitted []byte
	// Origins 是每个输出字节在上一阶段输出中的来源位置。
	Origins [][]int
}

// Result 是搜索或单次分析的结论。
type Result struct {
	// Status 是结论状态。
	Status Status
	// Input 是反例输入（仅 WitnessFound 时非空）。
	Input []byte
	// Hazards 是最终输出中出现的危险分隔符字节。
	Hazards []byte
	// Stages 是各阶段的输出；Stages[0] 即声明检查后的输入本身。
	Stages [stageCount][]byte
	// Trace 是评估器 B 的逐步执行记录。
	Trace []Step
	// Reason 在 InvalidConfig / VerificationFailed 时给出原因。
	Reason string
}

type byteSet map[byte]struct{}

func makeByteSet(bs []byte) byteSet {
	s := make(byteSet, len(bs))
	for _, b := range bs {
		s[b] = struct{}{}
	}
	return s
}

func (s byteSet) contains(b byte) bool {
	_, ok := s[b]
	return ok
}

func (s byteSet) sorted() []byte {
	out := make([]byte, 0, len(s))
	for b := range s {
		out = append(out, b)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// validate 检查配置并返回规整后的集合与上限。
func (c Config) validate() (alpha, danger, del byteSet, maxLen int, budget int64, reason string) {
	if len(c.Alphabet) == 0 {
		return nil, nil, nil, 0, 0, "empty alphabet"
	}
	if len(c.Dangerous) == 0 {
		return nil, nil, nil, 0, 0, "empty dangerous set"
	}
	alpha = makeByteSet(c.Alphabet)
	if len(alpha) != len(c.Alphabet) {
		return nil, nil, nil, 0, 0, "duplicate byte in alphabet"
	}
	danger = makeByteSet(c.Dangerous)
	if len(danger) != len(c.Dangerous) {
		return nil, nil, nil, 0, 0, "duplicate byte in dangerous set"
	}
	del = makeByteSet(c.Delete)
	if len(del) != len(c.Delete) {
		return nil, nil, nil, 0, 0, "duplicate byte in delete set"
	}
	maxLen = c.MaxLen
	if maxLen == 0 {
		maxLen = MaxWitnessLen
	}
	if maxLen < 0 || maxLen > MaxWitnessLen {
		return nil, nil, nil, 0, 0, "MaxLen out of range [1,12]"
	}
	budget = c.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	return alpha, danger, del, maxLen, budget, ""
}

// declaredCheckA 是评估器 A 的声明检查：输入不得直接含危险分隔符。
func declaredCheckA(in []byte, danger byteSet) bool {
	for _, b := range in {
		if danger.contains(b) {
			return false
		}
	}
	return true
}

// percentDecodeA 是评估器 A 的百分号解码阶段。
// 形如 %XX 且 XX 均为十六进制数字时解码为单字节（含非法 UTF-8 字节），
// 其余 '%' 原样保留；阶段内不递归解码。
func percentDecodeA(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if in[i] == '%' && i+2 < len(in) && isHex(in[i+1]) && isHex(in[i+2]) {
			out = append(out, hexVal(in[i+1])<<4|hexVal(in[i+2]))
			i += 3
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// backslashDecodeA 是评估器 A 的反斜线转义解码阶段。
// '\\' 后一个字节命中映射则输出映射值并消费两字节；
// 未命中映射或处于末尾时，反斜线原样保留。
func backslashDecodeA(in []byte, m map[byte]byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if in[i] == '\\' && i+1 < len(in) {
			if v, ok := m[in[i+1]]; ok {
				out = append(out, v)
				i += 2
				continue
			}
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// deleteA 是评估器 A 的删除阶段。
func deleteA(in []byte, del byteSet) []byte {
	out := make([]byte, 0, len(in))
	for _, b := range in {
		if !del.contains(b) {
			out = append(out, b)
		}
	}
	return out
}

// runPipelineA 让评估器 A 跑完整条管线，返回各阶段输出。
// Stages[0] 是声明检查通过后的输入本身；声明检查不通过返回 ok=false。
func runPipelineA(in []byte, c Config, danger, del byteSet) (stages [stageCount][]byte, ok bool) {
	if !declaredCheckA(in, danger) {
		return stages, false
	}
	stages[stageDeclared] = cloneBytes(in)
	stages[stagePercent] = percentDecodeA(in)
	stages[stageBackslash] = backslashDecodeA(stages[stagePercent], c.BackslashMap)
	stages[stageDelete] = deleteA(stages[stageBackslash], del)
	return stages, true
}
