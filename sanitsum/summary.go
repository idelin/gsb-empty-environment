package sanitsum

// fieldKey 是副作用字段槽位的内部标识。
type fieldKey struct {
	varName string
	field   string
}

// state 是块级抽象状态：变量与副作用字段各自携带“当前仍残留的污染类别位集”。
type state struct {
	vars   map[string]uint64
	fields map[fieldKey]uint64
}

// entryState 构造入口状态。knownFields 是图中静态出现的副作用字段，
// 它们按“携带全部污染类别”初始化，避免缺失槽位被误判为已净化；
// StoreStmt 动态写入产生的新字段按无污处理。
func entryState(g *FuncGraph, knownFields map[fieldKey]struct{}) *state {
	full := allBits(len(g.Categories))
	s := &state{
		vars:   make(map[string]uint64, len(g.Params)+len(g.Results)),
		fields: make(map[fieldKey]uint64, len(knownFields)),
	}
	// 入参默认携带全部污染类别；净化语句负责逐类清除。
	for _, p := range g.Params {
		s.vars[p] = full
	}
	for _, r := range g.Results {
		if _, ok := s.vars[r]; !ok {
			s.vars[r] = 0
		}
	}
	for fk := range knownFields {
		s.fields[fk] = full
	}
	return s
}

func (s *state) clone() *state {
	cp := &state{
		vars:   make(map[string]uint64, len(s.vars)),
		fields: make(map[fieldKey]uint64, len(s.fields)),
	}
	for k, v := range s.vars {
		cp.vars[k] = v
	}
	for k, v := range s.fields {
		cp.fields[k] = v
	}
	return cp
}

func (s *state) mergeIn(o *state) {
	for k, v := range o.vars {
		s.vars[k] |= v
	}
	for k, v := range o.fields {
		s.fields[k] |= v
	}
}

func (s *state) read(r Ref) uint64 {
	if r.Field != "" {
		return s.fields[fieldKey{r.Var, r.Field}]
	}
	return s.vars[r.Var]
}

func (s *state) write(r Ref, bits uint64) {
	if r.Field != "" {
		s.fields[fieldKey{r.Var, r.Field}] = bits
		return
	}
	s.vars[r.Var] = bits
}

func (s *state) clearBit(r Ref, bit uint64) {
	if r.Field != "" {
		k := fieldKey{r.Var, r.Field}
		s.fields[k] &^= bit
		return
	}
	s.vars[r.Var] &^= bit
}
