package taskpkg

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	maxCols       = 12
	maxCandidates = 4
	maxRelations  = 18
)

func errf(format string, args ...any) error {
	return fmt.Errorf("taskpkg: "+format, args...)
}

type maskRecord struct {
	picked []int
	mask   uint32
}

type solver struct {
	in         *Input
	rows, cols int
	rels       []preparedRel

	// parsed[c][k][r]：第 c 列第 k 个候选在第 r 行的规范值，缺失为 nil。
	parsed [][][]*big.Rat
	// viable[c]：该列可解析全部非缺失单元的候选下标。
	viable [][]int

	budget int64
	used   int64

	records  []maskRecord
	feasible []Combo
	allMask  uint32
}

// Solve 执行联合格式/单位推断，返回完整报告。输入不合法时返回 error；
// 预算耗尽不返回 error，而是给出 StatusIncomplete 的部分报告。
func Solve(in *Input) (*Report, error) {
	if in == nil {
		return nil, errf("输入为 nil")
	}
	s, err := newSolver(in)
	if err != nil {
		return nil, err
	}

	// 整列没有任何候选可解析全部单元时不可能存在可行组合；这属于数据/候选冲突，
	// 不是关系冲突，因此不产生 MUS。
	if bad := s.unparseableColumns(); len(bad) > 0 {
		return &Report{
			Status:             StatusInfeasible,
			Rows:               s.rows,
			Cols:               s.cols,
			BudgetUsed:         s.used,
			BudgetTotal:        s.budget,
			UnparseableColumns: bad,
		}, nil
	}

	incomplete := s.enumerate()

	rep := &Report{
		Rows:        s.rows,
		Cols:        s.cols,
		BudgetUsed:  s.used,
		BudgetTotal: s.budget,
	}
	if incomplete {
		rep.Status = StatusIncomplete
		rep.IncompleteReason = "枚举格式组合阶段预算耗尽，结果仅为部分可行组合，不得据此宣称唯一解释"
		s.fillAnalysis(rep)
		return rep, nil
	}

	if len(s.feasible) > 0 {
		rep.Status = StatusOK
		s.fillAnalysis(rep)
		return rep, nil
	}

	// 零可行解：按条数最少、ID 字典序决胜找冲突关系集。
	return s.analyzeConflicts(rep)
}

func newSolver(in *Input) (*solver, error) {
	cols := len(in.Candidates)
	if cols == 0 || cols > maxCols {
		return nil, errf("列数必须在 1-%d 之间，实际为 %d", maxCols, cols)
	}
	rows := len(in.Rows)
	if len(in.Keys) != rows {
		return nil, errf("业务键数量 %d 与数据行数 %d 不一致", len(in.Keys), rows)
	}
	if len(in.Relations) > maxRelations {
		return nil, errf("校验关系至多 %d 条，实际为 %d", maxRelations, len(in.Relations))
	}

	present := make([][]bool, rows)
	for r := range in.Rows {
		if len(in.Rows[r]) != cols {
			return nil, errf("第 %d 行宽度 %d 与列数 %d 不一致", r, len(in.Rows[r]), cols)
		}
		present[r] = make([]bool, cols)
		for c, raw := range in.Rows[r] {
			present[r][c] = strings.TrimSpace(raw) != ""
		}
	}

	s := &solver{in: in, rows: rows, cols: cols, budget: in.Budget}
	s.parsed = make([][][]*big.Rat, cols)
	s.viable = make([][]int, cols)
	for c := 0; c < cols; c++ {
		n := len(in.Candidates[c])
		if n == 0 || n > maxCandidates {
			return nil, errf("第 %d 列候选数必须在 1-%d 之间，实际为 %d", c, maxCandidates, n)
		}
		s.parsed[c] = make([][]*big.Rat, n)
		for k, cand := range in.Candidates[c] {
			if err := validateCandidate(cand); err != nil {
				return nil, errf("第 %d 列第 %d 个候选非法: %w", c, k, err)
			}
			vals := make([]*big.Rat, rows)
			feasible := true
			for r := 0; r < rows; r++ {
				if !present[r][c] {
					continue
				}
				v, err := ParseDecimal(in.Rows[r][c], cand.Decimal, cand.Grouping)
				if err != nil {
					feasible = false
					break
				}
				vals[r] = ScaleRat(v, cand.UnitScale)
			}
			if feasible {
				s.parsed[c][k] = vals
				s.viable[c] = append(s.viable[c], k)
			}
		}
	}

	ids := map[string]bool{}
	for _, rel := range in.Relations {
		if rel.ID == "" {
			return nil, errf("存在空 ID 的关系")
		}
		if ids[rel.ID] {
			return nil, errf("关系 ID 重复: %q", rel.ID)
		}
		ids[rel.ID] = true
	}
	rels, err := prepareRelations(in, present)
	if err != nil {
		return nil, err
	}
	s.rels = rels
	s.allMask = uint32(1)<<uint(len(rels)) - 1
	return s, nil
}

func validateCandidate(c Candidate) error {
	if c.Decimal != ',' && c.Decimal != '.' {
		return errf("小数符必须是 ',' 或 '.'")
	}
	switch c.Grouping {
	case GroupNone, GroupComma, GroupDot, GroupSpace:
	default:
		return errf("未知分组规则 %d", c.Grouping)
	}
	if g, ok := groupChar(c.Grouping); ok && g == c.Decimal {
		return errf("分组符与小数符相同")
	}
	if c.UnitScale != nil && c.UnitScale.Sign() == 0 {
		return errf("单位倍率不能为 0")
	}
	return nil
}

func (s *solver) unparseableColumns() []int {
	var bad []int
	for c := 0; c < s.cols; c++ {
		if len(s.viable[c]) == 0 {
			bad = append(bad, c)
		}
	}
	return bad
}

// charge 消耗一次检查预算；返回 true 表示预算已耗尽。
func (s *solver) charge() bool {
	if s.budget <= 0 {
		return false
	}
	s.used++
	return s.used > s.budget
}

// enumerate 按候选下标字典序做全组合 DFS。返回 true 表示预算耗尽、未完成。
func (s *solver) enumerate() bool {
	picked := make([]int, s.cols)
	var dfs func(col int) bool
	dfs = func(col int) bool {
		if col == s.cols {
			if s.charge() {
				return true
			}
			s.evaluate(picked)
			return false
		}
		for _, k := range s.viable[col] {
			picked[col] = k
			if dfs(col + 1) {
				return true
			}
		}
		return false
	}
	return dfs(0)
}

func (s *solver) evaluate(picked []int) {
	values := make([][]*big.Rat, s.rows)
	for r := 0; r < s.rows; r++ {
		values[r] = make([]*big.Rat, s.cols)
		for c := 0; c < s.cols; c++ {
			if v := s.parsed[c][picked[c]][r]; v != nil {
				values[r][c] = new(big.Rat).Set(v)
			}
		}
	}
	var mask uint32
	for i, rel := range s.rels {
		if rel.satisfied(values) {
			mask |= 1 << uint(i)
		}
	}
	shot := append([]int(nil), picked...)
	s.records = append(s.records, maskRecord{picked: shot, mask: mask})
	if mask == s.allMask {
		s.feasible = append(s.feasible, Combo{
			Picked:    shot,
			Values:    values,
			Signature: signatureOf(values),
		})
	}
}

func signatureOf(values [][]*big.Rat) string {
	var b strings.Builder
	for r, row := range values {
		if r > 0 {
			b.WriteByte('\x1f')
		}
		for c, v := range row {
			if c > 0 {
				b.WriteByte('\x1e')
			}
			if v == nil {
				b.WriteByte('_')
			} else {
				b.WriteString(v.RatString())
			}
		}
	}
	return b.String()
}

func (s *solver) fillAnalysis(rep *Report) {
	rep.Feasible = s.feasible
	rep.Classes = s.classes()
	rep.Undecidable = s.undecidable()
}

func (s *solver) classes() []EquivalenceClass {
	index := map[string]int{}
	var out []EquivalenceClass
	for _, combo := range s.feasible {
		idx, ok := index[combo.Signature]
		if !ok {
			cp := combo
			index[combo.Signature] = len(out)
			out = append(out, EquivalenceClass{
				Signature:      combo.Signature,
				Representative: &cp,
			})
			idx = len(out) - 1
		}
		out[idx].Members = append(out[idx].Members, append([]int(nil), combo.Picked...))
	}
	return out
}

// undecidable 找出在全部可行组合中出现过多个候选的列，并区分是否影响规范值。
func (s *solver) undecidable() []UndecidableColumn {
	type colInfo struct {
		indexes map[int]bool
		sigs    map[int]string
	}
	info := make([]colInfo, s.cols)
	for c := range info {
		info[c] = colInfo{indexes: map[int]bool{}, sigs: map[int]string{}}
	}
	for _, combo := range s.feasible {
		for c := 0; c < s.cols; c++ {
			k := combo.Picked[c]
			info[c].indexes[k] = true
			if _, ok := info[c].sigs[k]; !ok {
				info[c].sigs[k] = s.columnSignature(combo.Values, c)
			}
		}
	}
	var out []UndecidableColumn
	for c := 0; c < s.cols; c++ {
		if len(info[c].indexes) <= 1 {
			continue
		}
		var idxs []int
		for k := range info[c].indexes {
			idxs = append(idxs, k)
		}
		sort.Ints(idxs)
		distinct := map[string]bool{}
		for _, k := range idxs {
			distinct[info[c].sigs[k]] = true
		}
		out = append(out, UndecidableColumn{
			Column:           c,
			CandidateIndexes: idxs,
			ValueVaries:      len(distinct) > 1,
		})
	}
	return out
}

func (s *solver) columnSignature(values [][]*big.Rat, col int) string {
	var b strings.Builder
	for r := 0; r < s.rows; r++ {
		if r > 0 {
			b.WriteByte('\x1e')
		}
		if values[r][col] == nil {
			b.WriteByte('_')
		} else {
			b.WriteString(values[r][col].RatString())
		}
	}
	return b.String()
}

// subsetSatisfiable 判断是否存在枚举过的组合满足 mask 中的全部关系。
func (s *solver) subsetSatisfiable(mask uint32) bool {
	for _, rec := range s.records {
		if rec.mask&mask == mask {
			return true
		}
	}
	return false
}

// firstCombo 返回首个满足 mask 全部关系的组合及其规范值矩阵；不存在返回 nil。
func (s *solver) firstCombo(mask uint32) *Combo {
	for _, rec := range s.records {
		if rec.mask&mask != mask {
			continue
		}
		values := make([][]*big.Rat, s.rows)
		for r := 0; r < s.rows; r++ {
			values[r] = make([]*big.Rat, s.cols)
			for c := 0; c < s.cols; c++ {
				if v := s.parsed[c][rec.picked[c]][r]; v != nil {
					values[r][c] = new(big.Rat).Set(v)
				}
			}
		}
		return &Combo{
			Picked:    append([]int(nil), rec.picked...),
			Values:    values,
			Signature: signatureOf(values),
		}
	}
	return nil
}

// analyzeConflicts 在零可行解时检索 MUS，并为冲突集中每条关系准备删除后的可行解释。
func (s *solver) analyzeConflicts(rep *Report) (*Report, error) {
	n := len(s.rels)
	if n == 0 {
		rep.Status = StatusInfeasible
		return rep, nil
	}

	// 以 ID 字典序排列关系，保证子集决胜基于 ID 字典序。
	idOrder := make([]int, n)
	for i := range idOrder {
		idOrder[i] = i
	}
	sort.Slice(idOrder, func(a, b int) bool {
		return s.rels[idOrder[a]].id < s.rels[idOrder[b]].id
	})

	var musPositions []int
	found := false
	for size := 1; size <= n && !found; size++ {
		var walk func(start int, cur []int) bool
		walk = func(start int, cur []int) bool {
			if len(cur) == size {
				if s.charge() {
					return true
				}
				var mask uint32
				for _, pos := range cur {
					mask |= 1 << uint(idOrder[pos])
				}
				if !s.subsetSatisfiable(mask) {
					musPositions = append([]int(nil), cur...)
					found = true
					return true
				}
				return false
			}
			for pos := start; pos < n; pos++ {
				cur = append(cur, pos)
				if walk(pos+1, cur) {
					return true
				}
				cur = cur[:len(cur)-1]
			}
			return false
		}
		if walk(0, nil) && !found {
			rep.Status = StatusIncomplete
			rep.IncompleteReason = "冲突关系分析阶段预算耗尽，未定位到冲突集"
			rep.BudgetUsed = s.used
			rep.BudgetTotal = s.budget
			return rep, nil
		}
	}

	musIdx := make([]int, len(musPositions))
	var musMask uint32
	ids := make([]string, len(musPositions))
	for i, pos := range musPositions {
		musIdx[i] = idOrder[pos]
		musMask |= 1 << uint(musIdx[i])
		ids[i] = s.rels[musIdx[i]].id
	}
	sort.Strings(ids)

	conflict := Conflict{RelationIDs: ids}
	for _, relIdx := range musIdx {
		removedID := s.rels[relIdx].id
		restMask := musMask &^ (1 << uint(relIdx))
		w := Witness{RemovedRelation: removedID}
		// 优先给出满足“全部其余关系”的全局松弛解释；找不到时退化为仅满足
		// 冲突集其余关系的解释，并显式标注 ConflictOnly。
		if global := s.firstCombo(s.allMask &^ (1 << uint(relIdx))); global != nil {
			w.Combo = global
		} else if local := s.firstCombo(restMask); local != nil {
			w.Combo = local
			w.ConflictOnly = true
		} else {
			rep.Status = StatusIncomplete
			rep.IncompleteReason = "构造冲突删除后的可行解释时预算/数据异常"
			rep.BudgetUsed = s.used
			rep.BudgetTotal = s.budget
			return rep, nil
		}
		conflict.Witnesses = append(conflict.Witnesses, w)
	}

	rep.Status = StatusInfeasible
	rep.Conflicts = []Conflict{conflict}
	return rep, nil
}
