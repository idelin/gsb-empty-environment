package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
)

// Kind 标识有向对象图中一个节点的具体类型。
type Kind int

const (
	// KindNull 表示空值节点。
	KindNull Kind = iota
	// KindString 表示字符串节点，载荷在 Node.Str。
	KindString
	// KindNumber 表示数字节点，载荷在 Node.Num。
	KindNumber
	// KindArray 表示数组节点，载荷在 Node.Items。
	KindArray
	// KindDict 表示字典节点，载荷在 Node.Fields。
	KindDict
	// KindRef 表示显式引用（别名）节点，载荷在 Node.Target。
	KindRef
)

// Field 是字典中的一个键值对。
type Field struct {
	Key   string
	Value *Node
}

// Node 是有向对象图的节点。
//
// 攻击者可以控制图的拓扑与别名（数组元素、字典值、Ref.Target 以及指针
// 共享），但 Secret 由可信入口代码设置，攻击者无法移除。任何 Secret 节点
// 可达的全部节点都会在脱敏时被视为机密，即使该节点同时被另一条公开路径
// 引用也不会输出。
type Node struct {
	Kind   Kind
	Str    string
	Num    float64
	Items  []*Node
	Fields []Field
	Target *Node

	// Secret 是可信入口标签，为 true 时本节点及其整个可达子图均为机密。
	Secret bool
}

// Null 构造空值节点。
func Null() *Node { return &Node{Kind: KindNull} }

// String 构造字符串节点。
func String(v string) *Node { return &Node{Kind: KindString, Str: v} }

// Number 构造数字节点。
func Number(v float64) *Node { return &Node{Kind: KindNumber, Num: v} }

// Array 构造数组节点。
func Array(items ...*Node) *Node { return &Node{Kind: KindArray, Items: items} }

// Dict 构造字典节点。
func Dict(fields ...Field) *Node { return &Node{Kind: KindDict, Fields: fields} }

// Ref 构造指向 target 的显式引用（别名）节点。
func Ref(target *Node) *Node { return &Node{Kind: KindRef, Target: target} }

// MarkSecret 在节点上加盖可信机密标签并返回该节点。
func MarkSecret(n *Node) *Node {
	n.Secret = true
	return n
}

// 默认预算，零值 Options 时使用。
const (
	DefaultMaxBytes = 1 << 20
	DefaultMaxNodes = 100_000
)

// Options 控制脱敏输出的资源预算。零值字段会被替换为默认预算。
type Options struct {
	// MaxBytes 限制序列化结果的字节数，超限返回 ErrBudgetExceeded。
	MaxBytes int
	// MaxNodes 限制可达图中的去重节点数，超限返回 ErrBudgetExceeded。
	MaxNodes int
}

var (
	// ErrBudgetExceeded 表示超出预算，输出被完全拒绝（不返回任何片段）。
	ErrBudgetExceeded = errors.New("netsec: serialization budget exceeded")
	// ErrInvalidGraph 表示图中包含无法序列化的值（如 NaN/Inf）。
	ErrInvalidGraph = errors.New("netsec: invalid graph")
)

// redactedPlaceholder 是所有机密节点统一使用的固定占位符，不携带编号，
// 因此不会泄露机密节点的数量或身份。
const redactedPlaceholder = `{"$redacted":true}`

// Sanitize 对一组带可信标签的入口根节点做机密传播与脱敏序列化。
//
// roots 的键是可信入口名称。处理分两遍：第一遍从所有入口遍历整幅图，
// 把每个 Secret 节点的可达闭包标记为机密；第二遍只遍历公开节点并序列化为
// 确定性 JSON：公开共享/环用 $id/$ref 表示，编号严格按公开遍历顺序分配；
// 机密节点统一替换为固定占位符；字典按键字节排序。任一遍预算超限都会
// 完全拒绝输出，返回 nil 与 ErrBudgetExceeded。
func Sanitize(roots map[string]*Node, opts Options) ([]byte, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}

	confidential, err := markConfidential(roots, maxNodes)
	if err != nil {
		return nil, err
	}

	enc := &encoder{
		confidential: confidential,
		assigned:     make(map[*Node]int),
		maxBytes:     maxBytes,
	}
	if err := enc.encodeRoots(roots); err != nil {
		return nil, err
	}
	return bytes.Clone(enc.buf.Bytes()), nil
}

// children 返回节点的全部出边目标，nil 节点已由调用方过滤。
func children(n *Node) []*Node {
	switch n.Kind {
	case KindArray:
		return n.Items
	case KindDict:
		out := make([]*Node, 0, len(n.Fields))
		for _, f := range n.Fields {
			out = append(out, f.Value)
		}
		return out
	case KindRef:
		if n.Target != nil {
			return []*Node{n.Target}
		}
	}
	return nil
}

// markConfidential 是第一遍：遍历入口可达的整幅图，并把每个 Secret 节点
// 的可达闭包标记为机密。seen 对节点去重并承担节点数预算。
func markConfidential(roots map[string]*Node, maxNodes int) (map[*Node]bool, error) {
	confidential := make(map[*Node]bool)
	seen := make(map[*Node]bool)
	var stack []*Node

	for _, name := range sortedRootNames(roots) {
		n := roots[name]
		if n != nil && !seen[n] {
			seen[n] = true
			stack = append(stack, n)
		}
	}

	for len(stack) > 0 {
		if len(seen) > maxNodes {
			return nil, ErrBudgetExceeded
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if n.Secret && !confidential[n] {
			if err := flood(n, confidential, seen, maxNodes); err != nil {
				return nil, err
			}
		}

		for _, c := range children(n) {
			if c != nil && !seen[c] {
				seen[c] = true
				stack = append(stack, c)
			}
		}
	}
	return confidential, nil
}

// flood 从一个 Secret 节点出发，将其整个可达子图加入机密集合（含环）。
func flood(start *Node, confidential, seen map[*Node]bool, maxNodes int) error {
	work := []*Node{start}
	for len(work) > 0 {
		if len(seen) > maxNodes {
			return ErrBudgetExceeded
		}
		n := work[len(work)-1]
		work = work[:len(work)-1]
		if n == nil || confidential[n] {
			continue
		}
		confidential[n] = true
		if !seen[n] {
			seen[n] = true
		}
		for _, c := range children(n) {
			if c != nil && !confidential[c] {
				work = append(work, c)
			}
		}
	}
	return nil
}

type encoder struct {
	buf          bytes.Buffer
	confidential map[*Node]bool
	assigned     map[*Node]int
	nextID       int
	maxBytes     int
}

func (e *encoder) write(s string) error {
	e.buf.WriteString(s)
	if e.buf.Len() > e.maxBytes {
		return ErrBudgetExceeded
	}
	return nil
}

func (e *encoder) encodeRoots(roots map[string]*Node) error {
	if err := e.write(`{"roots":{`); err != nil {
		return err
	}
	names := sortedRootNames(roots)
	for i, name := range names {
		if i > 0 {
			if err := e.write(","); err != nil {
				return err
			}
		}
		key, err := jsonToken(name)
		if err != nil {
			return err
		}
		if err := e.write(key + ":"); err != nil {
			return err
		}
		if err := e.emit(roots[name]); err != nil {
			return err
		}
	}
	return e.write("}}")
}

// emit 输出单个节点。机密节点输出无编号占位符；公开节点首次出现时按公开
// 遍历顺序分配编号，再次出现（共享或回边）时输出 $ref。
func (e *encoder) emit(n *Node) error {
	if n == nil {
		return e.write("null")
	}
	if e.confidential[n] {
		return e.write(redactedPlaceholder)
	}
	// Ref 是透明别名：不占独立编号，身份完全等同于其目标，保证经别名与
	// 直接引用同一节点时共享同一个公开编号。
	if n.Kind == KindRef {
		return e.emit(n.Target)
	}
	if id, ok := e.assigned[n]; ok {
		return e.write(`{"$ref":` + strconv.Itoa(id) + "}")
	}

	id := e.nextID
	e.nextID++
	e.assigned[n] = id

	if err := e.write(`{"$id":` + strconv.Itoa(id) + `,"$type":"` + n.Kind.String() + `"`); err != nil {
		return err
	}

	switch n.Kind {
	case KindNull:
		// 无额外载荷。
	case KindString:
		tok, err := jsonToken(n.Str)
		if err != nil {
			return err
		}
		if err := e.write(`,"v":` + tok); err != nil {
			return err
		}
	case KindNumber:
		if math.IsNaN(n.Num) || math.IsInf(n.Num, 0) {
			return ErrInvalidGraph
		}
		if err := e.write(`,"v":` + strconv.FormatFloat(n.Num, 'g', -1, 64)); err != nil {
			return err
		}
	case KindArray:
		if err := e.write(`,"v":[`); err != nil {
			return err
		}
		for i, c := range n.Items {
			if i > 0 {
				if err := e.write(","); err != nil {
					return err
				}
			}
			if err := e.emit(c); err != nil {
				return err
			}
		}
		if err := e.write("]"); err != nil {
			return err
		}
	case KindDict:
		if err := e.write(`,"v":{`); err != nil {
			return err
		}
		if err := e.emitDictFields(n.Fields); err != nil {
			return err
		}
		if err := e.write("}"); err != nil {
			return err
		}
		// KindRef 已在 emit 入口透明展开，不会走到这里。
	}
	return e.write("}")
}

// emitDictFields 按键字节排序输出字段；重复键保留首次出现，保证确定性。
func (e *encoder) emitDictFields(fields []Field) error {
	if len(fields) == 0 {
		return nil
	}
	order := make([]Field, len(fields))
	copy(order, fields)
	sort.SliceStable(order, func(i, j int) bool {
		return order[i].Key < order[j].Key
	})
	prev := ""
	first := true
	for _, f := range order {
		if !first && f.Key == prev {
			continue
		}
		prev = f.Key
		first = false
		tok, err := jsonToken(f.Key)
		if err != nil {
			return err
		}
		if err := e.write(tok + ":"); err != nil {
			return err
		}
		if err := e.emit(f.Value); err != nil {
			return err
		}
		if err := e.write(","); err != nil {
			return err
		}
	}
	// 去掉最后一个逗号。
	e.buf.Truncate(e.buf.Len() - 1)
	return nil
}

func jsonToken(s string) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", ErrInvalidGraph
	}
	return string(b), nil
}

func sortedRootNames(roots map[string]*Node) []string {
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// String 返回 Kind 的稳定线化名。
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindString:
		return "string"
	case KindNumber:
		return "number"
	case KindArray:
		return "array"
	case KindDict:
		return "dict"
	case KindRef:
		return "ref"
	default:
		return "unknown"
	}
}
