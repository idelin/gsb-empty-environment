package joinselim

import (
	"errors"
	"fmt"
	"sort"
)

// Elimination records one applied join removal.
type Elimination struct {
	RemovedAlias string            `json:"removed_alias"`
	Table        string            `json:"table"`
	Rule         string            `json:"rule"`
	Key          []string          `json:"key,omitempty"`
	Replacement  map[string]string `json:"replacement,omitempty"`
}

// Result is the output of Rewrite.
type Result struct {
	// Query is the rewritten query. Its Where field is also exposed as Predicates.
	Query Query `json:"query"`
	// RetainedAliases are the aliases remaining in Query.
	RetainedAliases []string `json:"retained_aliases"`
	// RetainedTables are the retained alias/base-table pairs.
	RetainedTables []TableRef `json:"retained_tables"`
	// RemovedAliases are eliminated aliases in elimination order.
	RemovedAliases []string `json:"removed_aliases"`
	// ColumnMap maps every known original alias.column to its final reference
	// when that reference is still retained. Identity mappings are included.
	ColumnMap map[string]string `json:"column_map"`
	// RemappedColumns contains only non-identity column mappings.
	RemappedColumns map[string]string `json:"remapped_columns"`
	// Predicates is the rewritten top-level predicate list.
	Predicates   []Predicate   `json:"predicates"`
	Eliminations []Elimination `json:"eliminations"`
	Iterations   int           `json:"iterations"`
}

// Rewrite repeatedly applies all sound join-elimination rules.
//
// A nil schema is treated as an empty schema. The input query is not mutated.
func Rewrite(query Query, schema *Schema) (*Result, error) {
	if schema == nil {
		schema = NewSchema()
	}
	rw := &rewriter{
		schema:    schema,
		q:         cloneQuery(query),
		columnMap: map[string]Ref{},
	}
	if err := rw.initMappings(); err != nil {
		return nil, err
	}

	for {
		candidate := (*elimCandidate)(nil)
		// Reverse order naturally removes the child-most join of an FK chain
		// before the preceding join that references that alias.
		for idx := len(rw.q.Joins) - 1; idx >= 0; idx-- {
			if c, ok := rw.classify(idx); ok {
				candidate = &c
				break
			}
		}
		if candidate == nil {
			break
		}
		if err := rw.apply(*candidate); err != nil {
			return nil, err
		}
		rw.iterations++
	}

	return rw.result(), nil
}

// EliminateJoins is the descriptive entry point for the join-elimination pass.
func EliminateJoins(query Query, schema *Schema) (*Result, error) {
	return Rewrite(query, schema)
}

// Validate checks basic alias uniqueness and reference shape.
func (q Query) Validate() error {
	seen := map[string]bool{}
	if q.From.Alias == "" {
		return errors.New("query.from.alias must not be empty")
	}
	if seen[q.From.Alias] {
		return fmt.Errorf("duplicate alias %q", q.From.Alias)
	}
	seen[q.From.Alias] = true
	for i, j := range q.Joins {
		if j.Alias == "" {
			return fmt.Errorf("joins[%d].alias must not be empty", i)
		}
		if seen[j.Alias] {
			return fmt.Errorf("duplicate alias %q", j.Alias)
		}
		seen[j.Alias] = true
		if j.Type != InnerJoin && j.Type != LeftJoin {
			return fmt.Errorf("joins[%d]: unsupported join type %q", i, j.Type)
		}
		for _, eq := range j.On {
			if err := validateIntroducedRef(eq.Left, seen, i, "on"); err != nil {
				return err
			}
			if err := validateIntroducedRef(eq.Right, seen, i, "on"); err != nil {
				return err
			}
		}
		for _, p := range j.OnPredicates {
			for _, ref := range predicateRefs(p) {
				if err := validateIntroducedRef(ref, seen, i, "on_predicate"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateIntroducedRef(ref Ref, introduced map[string]bool, joinIndex int, clause string) error {
	if ref.Alias == "" {
		return fmt.Errorf("joins[%d].%s contains a reference with empty alias", joinIndex, clause)
	}
	if !introduced[ref.Alias] {
		return fmt.Errorf("joins[%d].%s references alias %q before it is introduced", joinIndex, clause, ref.Alias)
	}
	return nil
}

type rewriter struct {
	schema       *Schema
	q            Query
	columnMap    map[string]Ref
	removed      []string
	eliminations []Elimination
	iterations   int
}

type elimCandidate struct {
	joinIndex    int
	rule         string
	key          []string
	repl         map[string]Ref
	dropEquality map[int]bool
	fkName       string
}

func (rw *rewriter) initMappings() error {
	if err := rw.q.Validate(); err != nil {
		return err
	}
	aliases := append([]TableRef{rw.q.From}, tableRefs(rw.q.Joins)...)
	for _, tr := range aliases {
		if table, ok := rw.schema.Tables[tr.Table]; ok {
			for _, col := range table.Columns {
				ref := Col(tr.Alias, col.Name)
				rw.columnMap[ref.String()] = ref
			}
			continue
		}
	}
	for _, ref := range allQueryRefs(&rw.q) {
		if _, ok := rw.columnMap[ref.String()]; !ok {
			rw.columnMap[ref.String()] = ref
		}
	}
	return nil
}

func tableRefs(joins []Join) []TableRef {
	out := make([]TableRef, 0, len(joins))
	for _, j := range joins {
		out = append(out, TableRef{Alias: j.Alias, Table: j.Table})
	}
	return out
}

func cloneQuery(q Query) Query {
	out := q
	out.Projection = append([]Ref(nil), q.Projection...)
	out.Where = clonePredicates(q.Where)
	out.Joins = make([]Join, len(q.Joins))
	for i, j := range q.Joins {
		out.Joins[i] = j
		out.Joins[i].On = append([]Equality(nil), j.On...)
		out.Joins[i].OnPredicates = clonePredicates(j.OnPredicates)
	}
	return out
}

func clonePredicates(in []Predicate) []Predicate {
	out := make([]Predicate, len(in))
	for i, p := range in {
		out[i] = p
		if p.Other != nil {
			other := *p.Other
			out[i].Other = &other
		}
	}
	return out
}

func allQueryRefs(q *Query) []Ref {
	var refs []Ref
	refs = append(refs, q.Projection...)
	for _, p := range q.Where {
		refs = append(refs, predicateRefs(p)...)
	}
	for _, j := range q.Joins {
		for _, eq := range j.On {
			refs = append(refs, eq.Left, eq.Right)
		}
		for _, p := range j.OnPredicates {
			refs = append(refs, predicateRefs(p)...)
		}
	}
	return refs
}

func predicateRefs(p Predicate) []Ref {
	if p.Other != nil {
		return []Ref{p.Col, *p.Other}
	}
	return []Ref{p.Col}
}

func (rw *rewriter) tableOfAlias(alias string) (Table, bool) {
	if rw.q.From.Alias == alias {
		t, ok := rw.schema.Tables[rw.q.From.Table]
		return t, ok
	}
	for _, j := range rw.q.Joins {
		if j.Alias == alias {
			t, ok := rw.schema.Tables[j.Table]
			return t, ok
		}
	}
	return Table{}, false
}

func (rw *rewriter) physicalTableOfAlias(alias string) string {
	if rw.q.From.Alias == alias {
		return rw.q.From.Table
	}
	for _, j := range rw.q.Joins {
		if j.Alias == alias {
			return j.Table
		}
	}
	return ""
}

func (rw *rewriter) classify(idx int) (elimCandidate, bool) {
	j := rw.q.Joins[idx]
	if c, ok := rw.selfCandidate(idx, j); ok {
		return c, true
	}
	if j.Type == InnerJoin {
		if c, ok := rw.foreignKeyCandidate(idx, j); ok {
			return c, true
		}
	}
	if j.Type == LeftJoin {
		if c, ok := rw.leftCandidate(idx, j); ok {
			return c, true
		}
	}
	return elimCandidate{}, false
}

func (rw *rewriter) leftCandidate(idx int, j Join) (elimCandidate, bool) {
	joinCols := make([]string, 0, len(j.On))
	for _, eq := range j.On {
		if eq.Left.Alias == j.Alias {
			joinCols = append(joinCols, eq.Left.Column)
		}
		if eq.Right.Alias == j.Alias {
			joinCols = append(joinCols, eq.Right.Column)
		}
	}
	key, unique := rw.coversUniqueKey(j.Table, joinCols)
	if !unique && !rw.q.Distinct {
		return elimCandidate{}, false
	}
	if rw.aliasReferencedExternally(idx, j.Alias) {
		return elimCandidate{}, false
	}

	repl := map[string]Ref{}
	for _, eq := range j.On {
		if eq.Left.Alias == j.Alias && eq.Right.Alias != j.Alias {
			repl[eq.Left.String()] = eq.Right
		}
		if eq.Right.Alias == j.Alias && eq.Left.Alias != j.Alias {
			repl[eq.Right.String()] = eq.Left
		}
	}
	rule := "left-outer-distinct-duplicate-removal"
	if unique {
		rule = "left-outer-unique-key"
	}
	return elimCandidate{
		joinIndex: idx,
		rule:      rule,
		key:       key,
		repl:      repl,
	}, true
}

func (rw *rewriter) coversUniqueKey(tableName string, cols []string) ([]string, bool) {
	t, ok := rw.schema.Tables[tableName]
	if !ok {
		return nil, false
	}
	colSet := map[string]bool{}
	for _, c := range cols {
		colSet[c] = true
	}
	keys := make([][]string, 0, 1+len(t.UniqueKeys))
	if len(t.PrimaryKey) > 0 {
		keys = append(keys, t.PrimaryKey)
	}
	keys = append(keys, t.UniqueKeys...)
	// Try shorter keys first for stable output.
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) < len(keys[j]) })
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		covered := true
		for _, kc := range key {
			if !colSet[kc] {
				covered = false
				break
			}
		}
		if covered {
			return append([]string(nil), key...), true
		}
	}
	return nil, false
}

func (rw *rewriter) aliasReferencedExternally(joinIndex int, alias string) bool {
	for _, r := range rw.externalRefs(joinIndex) {
		if r.Alias == alias {
			return true
		}
	}
	return false
}

func (rw *rewriter) externalRefs(joinIndex int) []Ref {
	var refs []Ref
	refs = append(refs, rw.q.Projection...)
	for _, p := range rw.q.Where {
		refs = append(refs, predicateRefs(p)...)
	}
	for i, j := range rw.q.Joins {
		if i == joinIndex {
			continue
		}
		for _, eq := range j.On {
			refs = append(refs, eq.Left, eq.Right)
		}
		for _, p := range j.OnPredicates {
			refs = append(refs, predicateRefs(p)...)
		}
	}
	return refs
}

func (rw *rewriter) selfCandidate(idx int, j Join) (elimCandidate, bool) {
	if j.Type != InnerJoin {
		return elimCandidate{}, false
	}
	table, ok := rw.schema.Tables[j.Table]
	if !ok {
		return elimCandidate{}, false
	}
	keys := make([][]string, 0, 1+len(table.UniqueKeys))
	if len(table.PrimaryKey) > 0 {
		keys = append(keys, table.PrimaryKey)
	}
	keys = append(keys, table.UniqueKeys...)
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) < len(keys[j]) })

	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		drop := map[int]bool{}
		survivor := ""
		ok := true
		for _, keyCol := range key {
			found := false
			for eqIdx, eq := range j.On {
				other, matches := rw.samePhysicalKeyEquality(eq, j.Alias, keyCol)
				if !matches {
					continue
				}
				if survivor == "" {
					survivor = other.Alias
				} else if survivor != other.Alias {
					continue
				}
				drop[eqIdx] = true
				found = true
				break
			}
			if !found {
				ok = false
				break
			}
		}
		if !ok || survivor == "" {
			continue
		}

		repl := map[string]Ref{}
		for _, col := range table.Columns {
			repl[Col(j.Alias, col.Name).String()] = Col(survivor, col.Name)
		}
		// Be robust if the query references a column absent from schema metadata.
		for _, ref := range allQueryRefs(&rw.q) {
			if ref.Alias == j.Alias {
				if _, ok := repl[ref.String()]; !ok {
					repl[ref.String()] = Col(survivor, ref.Column)
				}
			}
		}

		nonNull := true
		for _, keyCol := range key {
			survivorRef := Col(survivor, keyCol)
			removedRef := Col(j.Alias, keyCol)
			if !rw.refKnownNotNull(survivorRef, j.OnPredicates) &&
				!rw.refKnownNotNull(removedRef, j.OnPredicates) {
				nonNull = false
				break
			}
		}
		if !nonNull {
			continue
		}

		return elimCandidate{
			joinIndex:    idx,
			rule:         "inner-self-unique-key-collapse",
			key:          append([]string(nil), key...),
			repl:         repl,
			dropEquality: drop,
		}, true
	}
	return elimCandidate{}, false
}

// samePhysicalKeyEquality returns the other alias when eq is alias.keyCol =
// otherAlias.keyCol and both aliases are occurrences of the same base table.
func (rw *rewriter) samePhysicalKeyEquality(eq Equality, removedAlias, keyCol string) (Ref, bool) {
	removedTable := rw.physicalTableOfAlias(removedAlias)
	match := func(removed Ref, other Ref) (Ref, bool) {
		if removed.Alias != removedAlias || removed.Column != keyCol {
			return Ref{}, false
		}
		if other.Column != keyCol || other.Alias == removedAlias {
			return Ref{}, false
		}
		if rw.physicalTableOfAlias(other.Alias) != removedTable {
			return Ref{}, false
		}
		return other, true
	}
	if other, ok := match(eq.Left, eq.Right); ok {
		return other, true
	}
	if other, ok := match(eq.Right, eq.Left); ok {
		return other, true
	}
	return Ref{}, false
}

func (rw *rewriter) foreignKeyCandidate(idx int, j Join) (elimCandidate, bool) {
	parentTable, ok := rw.schema.Tables[j.Table]
	if !ok {
		return elimCandidate{}, false
	}

	for _, fk := range rw.schema.ForeignKeys {
		if fk.ReferencedTable != j.Table && fk.ReferencedTable != parentTable.Name {
			continue
		}
		refCols := fk.ReferencedColumns
		if len(refCols) == 0 {
			refCols = parentTable.PrimaryKey
		}
		if len(refCols) == 0 || len(refCols) != len(fk.Columns) {
			continue
		}
		if _, unique := rw.coversUniqueKey(j.Table, refCols); !unique {
			continue
		}

		childAlias := ""
		repl := map[string]Ref{}
		drop := map[int]bool{}
		matched := true
		for colIdx, fkCol := range fk.Columns {
			parentCol := refCols[colIdx]
			found := false
			for eqIdx, eq := range j.On {
				childRef, ok := rw.fkEqualityChild(eq, j.Alias, parentCol, fkCol, fk.Table)
				if !ok {
					continue
				}
				if childAlias == "" {
					childAlias = childRef.Alias
				} else if childAlias != childRef.Alias {
					continue
				}
				drop[eqIdx] = true
				repl[Col(j.Alias, parentCol).String()] = childRef
				found = true
				break
			}
			if !found {
				matched = false
				break
			}
		}
		if !matched || childAlias == "" {
			continue
		}
		if rw.physicalTableOfAlias(childAlias) != fk.Table {
			continue
		}

		// Every reference to the parent alias must be a declared target key
		// column that is equivalent to the child FK column. Other parent
		// attributes cannot be synthesized on the child side.
		var parentRefs []Ref
		parentRefs = append(parentRefs, rw.externalRefs(idx)...)
		for _, eq := range j.On {
			parentRefs = append(parentRefs, eq.Left, eq.Right)
		}
		for _, p := range j.OnPredicates {
			parentRefs = append(parentRefs, predicateRefs(p)...)
		}
		if bad, ok := unsupportedAliasRef(parentRefs, j.Alias, repl); ok {
			_ = bad
			continue
		}

		allNotNull := true
		for _, fkCol := range fk.Columns {
			if !rw.refKnownNotNull(Col(childAlias, fkCol), j.OnPredicates) {
				allNotNull = false
				break
			}
		}
		if !allNotNull {
			continue
		}

		return elimCandidate{
			joinIndex:    idx,
			rule:         "inner-foreign-key-primary-key",
			key:          append([]string(nil), refCols...),
			repl:         repl,
			dropEquality: drop,
			fkName:       fk.Name,
		}, true
	}
	return elimCandidate{}, false
}

func (rw *rewriter) fkEqualityChild(eq Equality, parentAlias, parentCol, fkCol, childTable string) (Ref, bool) {
	match := func(parent Ref, child Ref) (Ref, bool) {
		if parent.Alias != parentAlias || parent.Column != parentCol {
			return Ref{}, false
		}
		if child.Column != fkCol || child.Alias == parentAlias {
			return Ref{}, false
		}
		if rw.physicalTableOfAlias(child.Alias) != childTable {
			return Ref{}, false
		}
		return child, true
	}
	if child, ok := match(eq.Left, eq.Right); ok {
		return child, true
	}
	if child, ok := match(eq.Right, eq.Left); ok {
		return child, true
	}
	return Ref{}, false
}

func unsupportedAliasRef(refs []Ref, alias string, repl map[string]Ref) (Ref, bool) {
	for _, ref := range refs {
		if ref.Alias != alias {
			continue
		}
		if _, ok := repl[ref.String()]; !ok {
			return ref, true
		}
	}
	return Ref{}, false
}

// refKnownNotNull proves that ref cannot be NULL after applying schema
// nullability and conjunctive null-rejecting predicates in Where or extra ON.
func (rw *rewriter) refKnownNotNull(ref Ref, joinPredicates []Predicate) bool {
	if tableName := rw.physicalTableOfAlias(ref.Alias); tableName != "" {
		if col, ok := rw.schema.Column(tableName, ref.Column); ok && !col.Nullable {
			return true
		}
	}
	for _, p := range rw.q.Where {
		if predicateProvesNotNull(p, ref) {
			return true
		}
	}
	for _, p := range joinPredicates {
		if predicateProvesNotNull(p, ref) {
			return true
		}
	}
	return false
}

func predicateProvesNotNull(p Predicate, ref Ref) bool {
	referencesRef := p.Col == ref || (p.Other != nil && *p.Other == ref)
	if !referencesRef {
		return false
	}
	switch p.Op {
	case OpNotNull:
		return p.Col == ref
	case OpEqual, OpNotEqual, OpLT, OpLE, OpGT, OpGE:
		// Every supported binary comparison is unknown when either operand is
		// NULL, so a surviving row has non-null operands.
		return true
	default:
		return false
	}
}

func (rw *rewriter) apply(c elimCandidate) error {
	idx := c.joinIndex
	removed := rw.q.Joins[idx]

	for originalKey, current := range rw.columnMap {
		if target, ok := c.repl[current.String()]; ok {
			rw.columnMap[originalKey] = target
		}
	}

	// Rewrite the retained query clauses.
	for i := range rw.q.Projection {
		rw.q.Projection[i] = mapRef(rw.q.Projection[i], c.repl)
	}
	for i := range rw.q.Where {
		mapPredicate(&rw.q.Where[i], c.repl)
	}
	for ji := range rw.q.Joins {
		if ji == idx {
			continue
		}
		for ei := range rw.q.Joins[ji].On {
			rw.q.Joins[ji].On[ei].Left = mapRef(rw.q.Joins[ji].On[ei].Left, c.repl)
			rw.q.Joins[ji].On[ei].Right = mapRef(rw.q.Joins[ji].On[ei].Right, c.repl)
		}
		for pi := range rw.q.Joins[ji].OnPredicates {
			mapPredicate(&rw.q.Joins[ji].OnPredicates[pi], c.repl)
		}
	}

	// Inner join ON predicates are equivalent to WHERE. Preserve every
	// non-FK/extra condition after column substitution. For self-collapse all
	// columns have replacements; for FK elimination only parent key columns do,
	// and classification has already rejected unsupported parent references.
	if c.rule != "left-outer-unique-key" && c.rule != "left-outer-distinct-duplicate-removal" {
		for ei, eq := range removed.On {
			if c.dropEquality[ei] {
				continue
			}
			left := mapRef(eq.Left, c.repl)
			right := mapRef(eq.Right, c.repl)
			rw.q.Where = append(rw.q.Where, EqualColumns(left, right))
		}
		for _, p := range removed.OnPredicates {
			mapped := p
			mapPredicate(&mapped, c.repl)
			rw.q.Where = append(rw.q.Where, mapped)
		}
	}

	rw.q.Joins = append(rw.q.Joins[:idx], rw.q.Joins[idx+1:]...)
	rw.removed = append(rw.removed, removed.Alias)

	replacement := map[string]string{}
	for from, to := range c.repl {
		replacement[from] = to.String()
	}
	rw.eliminations = append(rw.eliminations, Elimination{
		RemovedAlias: removed.Alias,
		Table:        removed.Table,
		Rule:         c.rule,
		Key:          c.key,
		Replacement:  replacement,
	})
	return nil
}

func mapRef(ref Ref, repl map[string]Ref) Ref {
	if to, ok := repl[ref.String()]; ok {
		return to
	}
	return ref
}

func mapPredicate(p *Predicate, repl map[string]Ref) {
	p.Col = mapRef(p.Col, repl)
	if p.Other != nil {
		other := mapRef(*p.Other, repl)
		p.Other = &other
	}
}

func (rw *rewriter) result() *Result {
	retainedAliases := []string{rw.q.From.Alias}
	retainedTables := []TableRef{rw.q.From}
	for _, j := range rw.q.Joins {
		retainedAliases = append(retainedAliases, j.Alias)
		retainedTables = append(retainedTables, TableRef{Alias: j.Alias, Table: j.Table})
	}
	retainedSet := map[string]bool{}
	for _, a := range retainedAliases {
		retainedSet[a] = true
	}

	columnMap := map[string]string{}
	remapped := map[string]string{}
	keys := make([]string, 0, len(rw.columnMap))
	for k := range rw.columnMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		to := rw.columnMap[k]
		if !retainedSet[to.Alias] {
			continue
		}
		columnMap[k] = to.String()
		if k != to.String() {
			remapped[k] = to.String()
		}
	}

	return &Result{
		Query:           rw.q,
		RetainedAliases: retainedAliases,
		RetainedTables:  retainedTables,
		RemovedAliases:  append([]string(nil), rw.removed...),
		ColumnMap:       columnMap,
		RemappedColumns: remapped,
		Predicates:      append([]Predicate(nil), rw.q.Where...),
		Eliminations:    append([]Elimination(nil), rw.eliminations...),
		Iterations:      rw.iterations,
	}
}
