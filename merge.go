package netsec

import (
	"fmt"
	"math/bits"
	"sort"
)

// FieldDomain 给出某个字段的全部合法取值。
// 取值顺序仅用于穷举枚举，不影响合并语义。
type FieldDomain struct {
	Name   string
	Values []string
}

// Rule 是有序首匹配规则表中的一条规则。
// Match 为若干字段相等条件的合取；命中本条时决策为 Decision。
// Match 为空（或 nil）匹配任意输入。规则按表顺序依次求值，首个命中者生效。
type Rule struct {
	Match    map[string]string
	Decision string
}

// Counterexample 是触发本地与远端语义冲突的最小部分输入。
// 未列出的字段取任意合法取值，冲突都依然成立。
type Counterexample struct {
	Input  map[string]string
	Base   string
	Local  string
	Remote string
}

// Conflict 表示本地与远端的独立改动互不相容，无法自动合并。
type Conflict struct {
	Counterexample Counterexample
}

func (e *Conflict) Error() string {
	keys := make([]string, 0, len(e.Counterexample.Input))
	for k := range e.Counterexample.Input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ce := e.Counterexample
	if ce.Local == "" {
		return fmt.Sprintf("netsec: conflicting decisions on partial input %v (decisions vary across unconstrained fields)", keys)
	}
	return fmt.Sprintf("netsec: conflicting decisions on input %v: local=%q remote=%q base=%q",
		keys, ce.Local, ce.Remote, ce.Base)
}

// Is 使 errors.Is(err, &Conflict{}) 成立。
func (e *Conflict) Is(target error) bool {
	_, ok := target.(*Conflict)
	return ok
}

// maxCombinations 限定穷举输入空间的上限。
const maxCombinations = 1_000_000

// Merge 对三张有序首匹配规则表做结构化三方合并。
//
// base 是共同祖先版本，local 与 remote 是各自独立修改后的版本，
// defaultDecision 是没有任何规则命中时采用的决策。domains 给出全部字段的
// 合法取值域，域内取值总数不得超过 1,000,000。
//
// 合并在穷举出的整个输入空间上逐点进行：仅当 local 与 remote 对同一输入都
// 偏离 base 且偏离结果不一致时判定为冲突（任意一方未改动的输入直接采用另一
// 方的决策）。判定基于规则表的完整决策函数，因此规则重排、删除与新增造成的
// 语义冲突都能被识别，不依赖文本层面的差异。
//
// 无冲突时返回合并后的规则表：其规则彼此互斥，按枚举顺序排列，首匹配语义与
// 逐点合并结果完全等价。有冲突时返回 *Conflict，其中携带最小反例输入。
func Merge(defaultDecision string, domains []FieldDomain, base, local, remote []Rule) ([]Rule, error) {
	if err := validateDomains(domains); err != nil {
		return nil, err
	}
	if err := validateRules(base, domains, "base"); err != nil {
		return nil, err
	}
	if err := validateRules(local, domains, "local"); err != nil {
		return nil, err
	}
	if err := validateRules(remote, domains, "remote"); err != nil {
		return nil, err
	}

	f := len(domains)
	sizes := make([]int, f)
	strides := make([]int, f)
	valueIndex := make(map[string]map[string]int, f)
	fieldPos := make(map[string]int, f)
	n := 1
	for i, d := range domains {
		sizes[i] = len(d.Values)
		strides[i] = n
		fieldPos[d.Name] = i
		vm := make(map[string]int, len(d.Values))
		for j, v := range d.Values {
			vm[v] = j
		}
		valueIndex[d.Name] = vm
		n *= sizes[i]
		if n > maxCombinations {
			return nil, fmt.Errorf("netsec: input space exceeds %d combinations", maxCombinations)
		}
	}

	codes := map[string]int{defaultDecision: 0}
	intern := func(decision string) int {
		if code, ok := codes[decision]; ok {
			return code
		}
		code := len(codes)
		codes[decision] = code
		return code
	}

	baseDecisions := evalTable(base, n, sizes, strides, fieldPos, valueIndex, intern)
	localDecisions := evalTable(local, n, sizes, strides, fieldPos, valueIndex, intern)
	remoteDecisions := evalTable(remote, n, sizes, strides, fieldPos, valueIndex, intern)

	conflictPos := -1
	for p := 0; p < n; p++ {
		b, l, r := baseDecisions[p], localDecisions[p], remoteDecisions[p]
		if l != b && r != b && l != r {
			conflictPos = p
			break
		}
	}
	if conflictPos >= 0 {
		return nil, buildConflict(conflictPos, f, sizes, strides, domains,
			baseDecisions, localDecisions, remoteDecisions, codes)
	}

	merged := make([]int, n)
	for p := 0; p < n; p++ {
		switch {
		case localDecisions[p] != baseDecisions[p]:
			merged[p] = localDecisions[p]
		case remoteDecisions[p] != baseDecisions[p]:
			merged[p] = remoteDecisions[p]
		default:
			merged[p] = baseDecisions[p]
		}
	}

	if decisionsEqual(merged, localDecisions) {
		return local, nil
	}
	if decisionsEqual(merged, remoteDecisions) {
		return remote, nil
	}
	if decisionsEqual(merged, baseDecisions) {
		return base, nil
	}

	decisionText := make([]string, len(codes))
	for decision, code := range codes {
		decisionText[code] = decision
	}
	return synthesize(merged, f, sizes, domains, decisionText), nil
}

func decisionsEqual(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateDomains(domains []FieldDomain) error {
	seenFields := make(map[string]bool, len(domains))
	for i, d := range domains {
		if d.Name == "" {
			return fmt.Errorf("netsec: domain %d has empty field name", i)
		}
		if seenFields[d.Name] {
			return fmt.Errorf("netsec: duplicate field %q", d.Name)
		}
		seenFields[d.Name] = true
		if len(d.Values) == 0 {
			return fmt.Errorf("netsec: field %q has empty domain", d.Name)
		}
		seenValues := make(map[string]bool, len(d.Values))
		for _, v := range d.Values {
			if seenValues[v] {
				return fmt.Errorf("netsec: duplicate value %q in domain of %q", v, d.Name)
			}
			seenValues[v] = true
		}
	}
	return nil
}

func validateRules(rules []Rule, domains []FieldDomain, side string) error {
	domainOf := make(map[string]map[string]bool, len(domains))
	for _, d := range domains {
		values := make(map[string]bool, len(d.Values))
		for _, v := range d.Values {
			values[v] = true
		}
		domainOf[d.Name] = values
	}
	for i, rule := range rules {
		for field, value := range rule.Match {
			values, ok := domainOf[field]
			if !ok {
				return fmt.Errorf("netsec: %s rule %d references unknown field %q", side, i, field)
			}
			if !values[value] {
				return fmt.Errorf("netsec: %s rule %d: value %q is not in domain of %q", side, i, value, field)
			}
		}
	}
	return nil
}

// evalTable 穷举整个输入空间，返回每个枚举位置上的首匹配决策编码。
// 决策码 0 保留给默认决策。
func evalTable(rules []Rule, n int, sizes, strides []int, fieldPos map[string]int,
	valueIndex map[string]map[string]int, intern func(string) int) []int {
	words := (n + 63) >> 6
	decisions := make([]int, n)
	match := make([]uint64, words)
	// 从最低优先级规则向前回填，高优先级规则自然覆盖低优先级规则。
	for i := len(rules) - 1; i >= 0; i-- {
		ruleMatchBitset(rules[i].Match, words, n, sizes, strides, fieldPos, valueIndex, match)
		decision := intern(rules[i].Decision)
		for w, word := range match {
			for word != 0 {
				trailing := word & -word
				pos := w<<6 + bits.TrailingZeros64(trailing)
				if pos < n {
					decisions[pos] = decision
				}
				word ^= trailing
			}
		}
	}
	return decisions
}

// ruleMatchBitset 将一条合取规则的匹配位置写入 dst（按 64 位字打包）。
func ruleMatchBitset(terms map[string]string, words, n int, sizes, strides []int,
	fieldPos map[string]int, valueIndex map[string]map[string]int, dst []uint64) {
	for w := 0; w < words; w++ {
		dst[w] = ^uint64(0)
	}
	if n&63 != 0 {
		dst[words-1] &= (uint64(1) << uint(n&63)) - 1
	}
	for field, value := range terms {
		fi := fieldPos[field]
		v := valueIndex[field][value]
		stride := strides[fi]
		block := stride * sizes[fi]
		for w := 0; w < words; w++ {
			dst[w] &= fieldWordMask(w, v, stride, block, n)
		}
	}
}

// fieldWordMask 返回第 w 个字中，某字段取值等于 value 的位置掩码。
// 该字段在行优先枚举中以 block 为周期，每个周期内的匹配段为
// [value*stride, value*stride+stride)。
func fieldWordMask(w, value, stride, block, n int) uint64 {
	base := w << 6
	var mask uint64
	for start := base - base%block; start < base+64 && start < n; start += block {
		lo := start + value*stride
		hi := lo + stride
		if lo < base {
			lo = base
		}
		if hi > base+64 {
			hi = base + 64
		}
		if hi > n {
			hi = n
		}
		if lo < hi {
			mask |= bitRange(uint(lo-base), uint(hi-base))
		}
	}
	return mask
}

func bitRange(lo, hi uint) uint64 {
	// hi 可能为 64，移位位数必须严格小于 64，因此拆成两个安全移位。
	return (^uint64(0) << lo) &^ (^uint64(0) << hi)
}

// buildConflict 从首个冲突位置出发，构造包含最小反例的 *Conflict。
func buildConflict(conflictPos, f int, sizes, strides []int, domains []FieldDomain,
	baseDecisions, localDecisions, remoteDecisions []int, codes map[string]int) error {
	fixed := make([]bool, f)
	for i := range fixed {
		fixed[i] = true
	}
	// anchor 是把当前所有自由字段取值清零后的子立方体角点；
	// witness 始终指向该子立方体内一个已知冲突点。逐字段尝试消去：
	// 若把字段 i 放开后整个子立方体仍处处冲突，则该字段无需约束。
	anchor := conflictPos
	witness := conflictPos
	for i := 0; i < f; i++ {
		corner := anchor - ((anchor/strides[i])%sizes[i])*strides[i]
		free := make([]int, 0, f)
		for j := 0; j < f; j++ {
			if !fixed[j] || j == i {
				free = append(free, j)
			}
		}
		if next, _, ok := scanSubcube(corner, free, sizes, strides,
			baseDecisions, localDecisions, remoteDecisions); ok {
			fixed[i] = false
			anchor = corner
			witness = next
		}
	}

	free := make([]int, 0, f)
	for i := 0; i < f; i++ {
		if !fixed[i] {
			free = append(free, i)
		}
	}
	witness, uniform, _ := scanSubcube(anchor, free, sizes, strides,
		baseDecisions, localDecisions, remoteDecisions)

	input := make(map[string]string, f)
	for i, d := range domains {
		if fixed[i] {
			digit := (witness / strides[i]) % sizes[i]
			input[d.Name] = d.Values[digit]
		}
	}

	decisionText := make([]string, len(codes))
	for decision, code := range codes {
		decisionText[code] = decision
	}
	var baseText, localText, remoteText string
	if uniform {
		baseText = decisionText[baseDecisions[witness]]
		localText = decisionText[localDecisions[witness]]
		remoteText = decisionText[remoteDecisions[witness]]
	}
	return &Conflict{Counterexample: Counterexample{
		Input:  input,
		Base:   baseText,
		Local:  localText,
		Remote: remoteText,
	}}
}

// scanSubcube 枚举 anchor 处、free 字段任意取值所张成的子立方体。
// 当且仅当子立方体内处处为冲突点时 ok 为 true，并返回其中一个见证位置；
// uniform 表示三张表的决策在子立方体内各自恒定。否则 ok 为 false。
func scanSubcube(anchor int, free, sizes, strides []int,
	baseDecisions, localDecisions, remoteDecisions []int) (int, bool, bool) {
	var scan func(k, pos int) (int, bool, bool)
	scan = func(k, pos int) (int, bool, bool) {
		if k == len(free) {
			b, l, r := baseDecisions[pos], localDecisions[pos], remoteDecisions[pos]
			if l != b && r != b && l != r {
				return pos, true, true
			}
			return 0, false, false
		}
		fi := free[k]
		base := pos
		witness := 0
		allUniform := true
		var refB, refL, refR int
		haveRef := false
		for value := 0; value < sizes[fi]; value++ {
			next, branchUniform, ok := scan(k+1, base+value*strides[fi])
			if !ok {
				return 0, false, false
			}
			witness = next
			allUniform = allUniform && branchUniform
			if !haveRef {
				refB, refL, refR = baseDecisions[next], localDecisions[next], remoteDecisions[next]
				haveRef = true
			} else if baseDecisions[next] != refB || localDecisions[next] != refL ||
				remoteDecisions[next] != refR {
				allUniform = false
			}
		}
		return witness, allUniform, true
	}
	return scan(0, anchor)
}

// synthesize 把逐点合并结果重建为规则彼此互斥的规范首匹配规则表。
// 递归始终按字段枚举序推进、逐字段分区，保证每个子立方体在数组中连续；
// 到达叶子（全部字段已固定）时，若决策不是默认决策（码 0）则落一条规则。
func synthesize(merged []int, f int, sizes []int,
	domains []FieldDomain, decisionText []string) []Rule {
	var rules []Rule
	// 行优先枚举中 domains[0] 是最内层字段、domains[f-1] 是最外层字段。
	// 递归按枚举从外层向内层推进（fieldIdx 从 f-1 递减到 0），
	// 这样切出的每个取值区块在数组中都是连续区间。
	// level 从 0 开始，对应处理的字段为 domains[f-1-level]。
	var build func(block, blockSize, level int, fixed map[string]string)
	build = func(block, blockSize, level int, fixed map[string]string) {
		if level == f {
			decision := merged[block]
			if decision != 0 {
				match := make(map[string]string, len(fixed))
				for k, v := range fixed {
					match[k] = v
				}
				rules = append(rules, Rule{Match: match, Decision: decisionText[decision]})
			}
			return
		}
		fieldIdx := f - 1 - level
		field := domains[fieldIdx]
		childSize := blockSize / sizes[fieldIdx]
		for value := 0; value < sizes[fieldIdx]; value++ {
			childBlock := block + value*childSize
			// 该取值区块决策一致则剪枝落规则，否则继续按后续字段分区。
			if uniformRange(childBlock, childSize, merged) {
				if merged[childBlock] != 0 {
					match := make(map[string]string, len(fixed)+1)
					for k, v := range fixed {
						match[k] = v
					}
					match[field.Name] = field.Values[value]
					rules = append(rules, Rule{Match: match, Decision: decisionText[merged[childBlock]]})
				}
				continue
			}
			childFixed := make(map[string]string, len(fixed)+1)
			for k, v := range fixed {
				childFixed[k] = v
			}
			childFixed[field.Name] = field.Values[value]
			build(childBlock, childSize, level+1, childFixed)
		}
	}
	build(0, len(merged), 0, map[string]string{})
	return rules
}

// uniformRange 检查数组区间 [start, start+size) 内决策是否完全一致。
func uniformRange(start, size int, merged []int) bool {
	first := merged[start]
	for p := start + 1; p < start+size; p++ {
		if merged[p] != first {
			return false
		}
	}
	return true
}
