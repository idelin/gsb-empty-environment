package tenantfilter

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const tenantArgumentMarker = "\x00tenantfilter-tenant-argument\x00"

type edit struct {
	at   int
	seq  int
	text string
}

type span struct {
	start int
	end   int
}

type cteDefinition struct {
	name        string
	scopeEnd    int
	visibleFrom int
}

type tableReference struct {
	qualifier string
	column    string
}

func (g *Guard) rewrite(query string, args []any, tenant any) (string, []any, error) {
	tokens, err := lexSQL(query)
	if err != nil {
		return "", nil, err
	}

	ranges := statementRanges(tokens)
	if len(ranges) == 0 {
		return "", nil, fmt.Errorf("%w: empty query", ErrMalformedQuery)
	}
	if len(ranges) > 1 {
		return "", nil, ErrMultipleStatements
	}
	start, end := ranges[0].start, ranges[0].end

	kind, err := statementKind(tokens, start, end)
	if err != nil {
		return "", nil, err
	}
	if err := rejectBareTableQueries(tokens, start, end); err != nil {
		return "", nil, err
	}
	ctes, err := collectCTEs(tokens, start, end)
	if err != nil {
		return "", nil, err
	}

	var edits []edit
	switch kind {
	case "select":
		edits, err = g.guardSelects(tokens, start, end, ctes)
	case "update":
		edits, err = g.guardUpdate(tokens, start, end, ctes)
	case "delete":
		edits, err = g.guardDelete(tokens, start, end, ctes)
	default:
		return "", nil, fmt.Errorf("%w: statement starts with %q; INSERT and DDL must use a typed, tenant-aware path", ErrUnsupportedQuery, kind)
	}
	if err != nil {
		return "", nil, err
	}

	if kind == "update" || kind == "delete" {
		subqueryEdits, subqueryErr := g.guardSelects(tokens, start, end, ctes)
		if subqueryErr != nil {
			return "", nil, subqueryErr
		}
		edits = append(edits, subqueryEdits...)
	}

	style, err := g.choosePlaceholderStyle(tokens)
	if err != nil {
		return "", nil, err
	}
	return render(tokens, edits, args, tenant, style)
}

func rejectBareTableQueries(tokens []token, start, end int) error {
	for i := start; i < end; i++ {
		if !isWord(tokens[i], "table") {
			continue
		}
		previous := prevSignificant(tokens, i-1, start)
		queryStart := previous < start || tokens[previous].text == "("
		if !queryStart && tokens[previous].kind == tokWord {
			switch tokens[previous].lower {
			case "union", "except", "intersect":
				queryStart = true
			}
		}
		if !queryStart {
			continue
		}
		next := nextSignificant(tokens, i+1, end)
		if next < end && isIdent(tokens[next]) {
			return fmt.Errorf("%w: TABLE query expressions cannot be tenant guarded", ErrUnsupportedQuery)
		}
	}
	return nil
}

type statementRange struct{ start, end int }

func statementRanges(tokens []token) []statementRange {
	var ranges []statementRange
	start := 0
	for i := range tokens {
		if tokens[i].text == ";" && tokens[i].depth == 0 {
			if hasContent(tokens, start, i) {
				ranges = append(ranges, statementRange{start, i})
			}
			start = i + 1
		}
	}
	if hasContent(tokens, start, len(tokens)) {
		ranges = append(ranges, statementRange{start, len(tokens)})
	}
	return ranges
}

func hasContent(tokens []token, start, end int) bool {
	for i := start; i < end; i++ {
		if isSignificant(tokens[i]) {
			return true
		}
	}
	return false
}

func statementKind(tokens []token, start, end int) (string, error) {
	depth := 0
	if first := nextSignificant(tokens, start, end); first < end {
		depth = tokens[first].depth
	}
	for i := start; i < end; i++ {
		if tokens[i].depth != depth || tokens[i].kind != tokWord {
			continue
		}
		switch tokens[i].lower {
		case "select", "update", "delete", "insert", "replace", "merge", "create", "alter", "drop", "truncate", "explain", "pragma":
			return tokens[i].lower, nil
		}
	}
	return "", fmt.Errorf("%w: no recognizable statement keyword", ErrUnsupportedQuery)
}

func kindAtDepth(tokens []token, start, end, depth int) (string, error) {
	for i := start; i < end; i++ {
		if tokens[i].depth != depth || tokens[i].kind != tokWord {
			continue
		}
		switch tokens[i].lower {
		case "select", "update", "delete", "insert", "replace", "merge", "values", "with":
			return tokens[i].lower, nil
		}
	}
	return "", fmt.Errorf("%w: empty CTE body", ErrMalformedQuery)
}

func collectCTEs(tokens []token, start, end int) ([]cteDefinition, error) {
	enclosing := enclosingOpens(tokens)
	var definitions []cteDefinition

	for i := start; i < end; i++ {
		if !isWord(tokens[i], "with") || !isCTEStart(tokens, i, start) {
			continue
		}
		scopeEnd := end
		if open := enclosing[i]; open >= 0 && tokens[open].match >= 0 {
			scopeEnd = tokens[open].match
		}
		j := nextSignificant(tokens, i+1, scopeEnd)
		recursive := false
		if j < scopeEnd && isWord(tokens[j], "recursive") {
			recursive = true
			j = nextSignificant(tokens, j+1, scopeEnd)
		}
		for j < scopeEnd {
			nameIndex := nextSignificant(tokens, j, scopeEnd)
			if nameIndex >= scopeEnd || !isIdent(tokens[nameIndex]) {
				return nil, fmt.Errorf("%w: expected CTE name", ErrMalformedQuery)
			}
			name := identifierKey(tokens[nameIndex])

			j = nextSignificant(tokens, nameIndex+1, scopeEnd)
			if j < scopeEnd && tokens[j].text == "(" {
				if tokens[j].match < 0 {
					return nil, fmt.Errorf("%w: malformed CTE column list", ErrMalformedQuery)
				}
				j = nextSignificant(tokens, tokens[j].match+1, scopeEnd)
			}
			if j >= scopeEnd || !isWord(tokens[j], "as") {
				return nil, fmt.Errorf("%w: expected AS after CTE name", ErrMalformedQuery)
			}
			j = nextSignificant(tokens, j+1, scopeEnd)
			for j < scopeEnd && (isWord(tokens[j], "not") || isWord(tokens[j], "materialized")) {
				j = nextSignificant(tokens, j+1, scopeEnd)
			}
			if j >= scopeEnd || tokens[j].text != "(" || tokens[j].match < 0 {
				return nil, fmt.Errorf("%w: expected parenthesized CTE body", ErrMalformedQuery)
			}
			bodyOpen := j
			bodyClose := tokens[j].match
			bodyKind, err := kindAtDepth(tokens, bodyOpen+1, bodyClose, tokens[bodyOpen].depth+1)
			if err != nil {
				return nil, err
			}
			switch bodyKind {
			case "select", "with", "values":
			default:
				return nil, fmt.Errorf("%w: CTE %q body is %s", ErrUnsupportedQuery, name, bodyKind)
			}
			visibleFrom := bodyClose + 1
			if recursive {
				visibleFrom = i
			}
			definitions = append(definitions, cteDefinition{
				name:        name,
				scopeEnd:    scopeEnd,
				visibleFrom: visibleFrom,
			})

			j = nextSignificant(tokens, bodyClose+1, scopeEnd)
			if j < scopeEnd && tokens[j].text == "," {
				j = nextSignificant(tokens, j+1, scopeEnd)
				continue
			}
			if j < scopeEnd {
				if tokens[j].kind != tokWord {
					return nil, fmt.Errorf("%w: unexpected token after CTE body", ErrMalformedQuery)
				}
				switch tokens[j].lower {
				case "select", "values", "with":
				default:
					return nil, fmt.Errorf("%w: CTE-scoped statement %q is not supported", ErrUnsupportedQuery, tokens[j].lower)
				}
			}
			break
		}
	}
	return definitions, nil
}

func (g *Guard) isCTEReference(parts []token, position int, ctes []cteDefinition) bool {
	if len(parts) != 1 {
		return false
	}
	name := identifierKey(parts[0])
	for _, cte := range ctes {
		if cte.name == name && position >= cte.visibleFrom && position < cte.scopeEnd {
			return true
		}
	}
	return false
}

type selectClauses struct {
	from    span
	where   span
	having  span
	qualify span
	union   int
}

func findSelectClauses(tokens []token, selectIndex, scopeEnd int) selectClauses {
	depth := tokens[selectIndex].depth
	marks := map[string]int{}
	union := scopeEnd
	for i := selectIndex + 1; i < scopeEnd; i++ {
		if tokens[i].depth != depth || tokens[i].kind != tokWord {
			continue
		}
		switch tokens[i].lower {
		case "union", "except", "intersect":
			union = i
			i = scopeEnd
		case "from":
			if _, exists := marks["from"]; !exists {
				marks["from"] = i
			}
		case "where":
			if _, exists := marks["where"]; !exists {
				marks["where"] = i
			}
		case "group":
			if _, exists := marks["group"]; !exists {
				marks["group"] = i
			}
		case "having":
			if _, exists := marks["having"]; !exists {
				marks["having"] = i
			}
		case "qualify":
			if _, exists := marks["qualify"]; !exists {
				marks["qualify"] = i
			}
		case "window":
			if _, exists := marks["window"]; !exists {
				marks["window"] = i
			}
		case "order":
			if _, exists := marks["order"]; !exists {
				marks["order"] = i
			}
		case "limit":
			if _, exists := marks["limit"]; !exists {
				marks["limit"] = i
			}
		case "offset":
			if _, exists := marks["offset"]; !exists {
				marks["offset"] = i
			}
		case "fetch":
			if _, exists := marks["fetch"]; !exists {
				marks["fetch"] = i
			}
		case "for":
			if _, exists := marks["for"]; !exists {
				marks["for"] = i
			}
		case "returning":
			if _, exists := marks["returning"]; !exists {
				marks["returning"] = i
			}
		case "option":
			if _, exists := marks["option"]; !exists {
				marks["option"] = i
			}
		}
	}

	boundaries := make([]int, 0, len(marks)+1)
	for _, mark := range marks {
		if mark >= union {
			continue
		}
		boundaries = append(boundaries, mark)
	}
	boundaries = append(boundaries, union)
	sort.Ints(boundaries)

	nextBoundary := func(after int) int {
		for _, boundary := range boundaries {
			if boundary > after {
				return boundary
			}
		}
		return scopeEnd
	}

	var clauses selectClauses
	clauses.union = union
	if mark, ok := marks["from"]; ok && mark < union {
		clauses.from = span{start: mark + 1, end: nextBoundary(mark)}
	}
	if mark, ok := marks["where"]; ok && mark < union {
		clauses.where = span{start: mark + 1, end: nextBoundary(mark)}
	}
	if mark, ok := marks["having"]; ok && mark < union {
		clauses.having = span{start: mark + 1, end: nextBoundary(mark)}
	}
	if mark, ok := marks["qualify"]; ok && mark < union {
		clauses.qualify = span{start: mark + 1, end: nextBoundary(mark)}
	}
	return clauses
}

func (g *Guard) guardSelects(tokens []token, start, end int, ctes []cteDefinition) ([]edit, error) {
	enclosing := enclosingOpens(tokens)
	var edits []edit
	for i := start; i < end; i++ {
		if !isWord(tokens[i], "select") {
			continue
		}
		scopeEnd := end
		if open := enclosing[i]; open >= 0 && tokens[open].match >= 0 {
			scopeEnd = tokens[open].match
		}
		blockEdits, err := g.guardSelectAt(tokens, i, scopeEnd, ctes)
		if err != nil {
			return nil, err
		}
		edits = append(edits, blockEdits...)
	}
	return edits, nil
}

func (g *Guard) guardSelectAt(tokens []token, selectIndex, scopeEnd int, ctes []cteDefinition) ([]edit, error) {
	clauses := findSelectClauses(tokens, selectIndex, scopeEnd)
	depth := tokens[selectIndex].depth

	if clauses.where.start != 0 {
		if err := g.rejectTenantReferences(tokens, clauses.where.start, clauses.where.end); err != nil {
			return nil, err
		}
	}
	if clauses.having.start != 0 {
		if err := g.rejectTenantReferences(tokens, clauses.having.start, clauses.having.end); err != nil {
			return nil, err
		}
	}
	if clauses.qualify.start != 0 {
		if err := g.rejectTenantReferences(tokens, clauses.qualify.start, clauses.qualify.end); err != nil {
			return nil, err
		}
	}

	if clauses.from.start == 0 {
		return nil, nil
	}

	refs, predicateSpans, err := g.parseFrom(tokens, clauses.from.start, clauses.from.end, depth, ctes)
	if err != nil {
		return nil, err
	}
	for _, predicate := range predicateSpans {
		if err := g.rejectTenantReferences(tokens, predicate.start, predicate.end); err != nil {
			return nil, err
		}
	}

	if len(refs) == 0 {
		return nil, nil
	}
	predicates := make([]string, 0, len(refs))
	for _, ref := range refs {
		predicates = append(predicates, ref.qualifier+"."+ref.column+" = "+tenantArgumentMarker)
	}
	expression := strings.Join(predicates, " AND ")

	if clauses.where.start != 0 {
		edits := []edit{
			{at: clauses.where.start, text: " ("},
			{at: clauses.where.end, text: ") AND (" + expression + ") "},
		}
		return edits, nil
	}
	return []edit{{at: clauses.from.end, text: " WHERE (" + expression + ") "}}, nil
}

func (g *Guard) rejectTenantReferences(tokens []token, start, end int) error {
	for i := start; i < end; i++ {
		if tokens[i].text == "(" && tokens[i].match >= 0 && tokens[i].match < end && isSubqueryStart(tokens, i) {
			i = tokens[i].match
			continue
		}
		if !isIdent(tokens[i]) {
			continue
		}
		next := nextSignificant(tokens, i+1, end)
		if next < end && tokens[next].text == "." {
			continue
		}
		name := strings.ToLower(identifierValue(tokens[i]))
		if _, forbidden := g.tenantColumns[name]; forbidden {
			return &OverrideError{Offset: tokens[i].start, Identifier: identifierValue(tokens[i])}
		}
	}
	return nil
}

func isSubqueryStart(tokens []token, open int) bool {
	next := nextSignificant(tokens, open+1, len(tokens))
	if next >= len(tokens) {
		return false
	}
	return isWord(tokens[next], "select") || isWord(tokens[next], "with") || isWord(tokens[next], "values")
}

func (g *Guard) parseFrom(tokens []token, start, end, depth int, ctes []cteDefinition) ([]tableReference, []span, error) {
	var refs []tableReference
	var predicateSpans []span
	seen := make(map[string]struct{})
	expectSource := true

	for i := start; i < end; {
		i = nextSignificant(tokens, i, end)
		if i >= end {
			break
		}
		tok := tokens[i]
		if tok.depth != depth {
			i++
			continue
		}
		if tok.text == "," {
			expectSource = true
			i++
			continue
		}
		if tok.kind == tokWord && isJoinKeyword(tok.lower) {
			if tok.lower == "join" || tok.lower == "straight_join" || tok.lower == "apply" {
				expectSource = true
			}
			i++
			continue
		}
		if tok.kind == tokWord && (tok.lower == "on" || tok.lower == "using") {
			conditionStart := i + 1
			j := conditionStart
			for j < end {
				if tokens[j].depth == depth {
					if tokens[j].text == "," || (tokens[j].kind == tokWord && isJoinKeyword(tokens[j].lower)) {
						break
					}
				}
				j++
			}
			predicateSpans = append(predicateSpans, span{start: conditionStart, end: j})
			i = j
			expectSource = false
			continue
		}
		if tok.kind == tokWord && (tok.lower == "lateral" || tok.lower == "only") {
			i++
			continue
		}
		if !expectSource {
			i++
			continue
		}
		if tok.text == "(" {
			if tok.match < 0 {
				return nil, nil, fmt.Errorf("%w: malformed table source", ErrMalformedQuery)
			}
			closeIndex := tok.match
			inner := nextSignificant(tokens, i+1, closeIndex)
			if inner < closeIndex && (isWord(tokens[inner], "select") || isWord(tokens[inner], "with") || isWord(tokens[inner], "values")) {
				_, i = parseAlias(tokens, closeIndex+1, end)
				expectSource = false
				continue
			}
			innerRefs, innerSpans, err := g.parseFrom(tokens, i+1, closeIndex, depth+1, ctes)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, innerRefs...)
			predicateSpans = append(predicateSpans, innerSpans...)
			_, i = parseAlias(tokens, closeIndex+1, end)
			expectSource = false
			continue
		}
		if !isIdent(tok) {
			return nil, nil, fmt.Errorf("%w: unexpected token %q in FROM clause", ErrMalformedQuery, tok.text)
		}

		tableStart := i
		parts, next := readQualifiedName(tokens, i, end)
		afterName := nextSignificant(tokens, next, end)
		if afterName < end && tokens[afterName].text == "(" {
			return nil, nil, fmt.Errorf("%w: table-valued functions are not tenant guarded", ErrUnsupportedQuery)
		}

		alias, next := parseAlias(tokens, next, end)
		if alias == "" {
			alias = parts[len(parts)-1].text
		}
		i = next
		expectSource = false

		if g.isCTEReference(parts, tableStart, ctes) {
			continue
		}

		names := make([]string, 0, len(parts))
		for _, part := range parts {
			names = append(names, identifierKey(part))
		}
		table := strings.Join(names, ".")
		unqualified := names[len(names)-1]
		column, scoped := g.columnFor(table, unqualified)
		if !scoped {
			continue
		}
		key := alias + "." + column
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, tableReference{qualifier: alias, column: column})
	}
	return refs, predicateSpans, nil
}

func isCTEStart(tokens []token, withIndex, start int) bool {
	previous := prevSignificant(tokens, withIndex-1, start)
	if previous < start {
		return true
	}
	if tokens[previous].text == "(" || tokens[previous].text == ";" {
		return true
	}
	return tokens[previous].kind == tokWord &&
		(tokens[previous].lower == "union" || tokens[previous].lower == "except" || tokens[previous].lower == "intersect")
}

func readQualifiedName(tokens []token, start, end int) ([]token, int) {
	parts := []token{tokens[start]}
	i := start + 1
	for {
		dot := nextSignificant(tokens, i, end)
		if dot >= end || tokens[dot].text != "." {
			return parts, i
		}
		part := nextSignificant(tokens, dot+1, end)
		if part >= end || !isIdent(tokens[part]) {
			return parts, i
		}
		parts = append(parts, tokens[part])
		i = part + 1
	}
}

func parseAlias(tokens []token, start, end int) (string, int) {
	i := nextSignificant(tokens, start, end)
	if i >= end {
		return "", start
	}
	if isWord(tokens[i], "as") {
		alias := nextSignificant(tokens, i+1, end)
		if alias < end && isIdent(tokens[alias]) {
			return tokens[alias].text, alias + 1
		}
		return "", start
	}
	if isIdent(tokens[i]) && !isAliasTerminator(tokens[i]) {
		return tokens[i].text, i + 1
	}
	return "", start
}

func isJoinKeyword(word string) bool {
	switch word {
	case "join", "inner", "left", "right", "full", "outer", "cross", "natural", "straight_join", "apply":
		return true
	default:
		return false
	}
}

func isAliasTerminator(tok token) bool {
	if tok.kind != tokWord {
		return false
	}
	switch tok.lower {
	case "as", "on", "using", "join", "inner", "left", "right", "full", "outer",
		"cross", "natural", "where", "group", "having", "qualify", "window",
		"order", "limit", "offset", "fetch", "for", "union", "except",
		"intersect", "returning", "tablesample", "indexed", "index", "not", "set",
		"values", "lateral", "with", "partition", "use", "force", "ignore", "apply":
		return true
	default:
		return false
	}
}

func (g *Guard) guardUpdate(tokens []token, start, end int, ctes []cteDefinition) ([]edit, error) {
	updateIndex := nextSignificant(tokens, start, end)
	if updateIndex >= end || !isWord(tokens[updateIndex], "update") {
		return nil, fmt.Errorf("%w: expected UPDATE", ErrMalformedQuery)
	}
	i := nextSignificant(tokens, updateIndex+1, end)
	if i < end && isWord(tokens[i], "only") {
		i = nextSignificant(tokens, i+1, end)
	}
	if i >= end || !isIdent(tokens[i]) {
		return nil, fmt.Errorf("%w: expected UPDATE target table", ErrMalformedQuery)
	}
	targetIndex := i
	parts, next := readQualifiedName(tokens, i, end)
	alias, next := parseAlias(tokens, next, end)
	if alias == "" {
		alias = parts[len(parts)-1].text
	}

	target, ok, err := g.referenceFor(parts, alias, targetIndex, ctes)
	if err != nil {
		return nil, err
	}
	firstClause := nextSignificant(tokens, next, end)
	if firstClause >= end || !isWord(tokens[firstClause], "set") {
		return nil, fmt.Errorf("%w: UPDATE with joined or multi-table targets is not supported", ErrUnsupportedQuery)
	}
	refs := []tableReference{}
	if ok {
		refs = append(refs, target)
	}

	marks, union := clauseMarks(tokens, next, end, tokens[updateIndex].depth)
	setMark, hasSet := marks["set"]
	if !hasSet {
		return nil, fmt.Errorf("%w: UPDATE without SET", ErrMalformedQuery)
	}

	setEnd := nextClauseAfter(marks, union, setMark, end)
	if err := g.rejectTenantReferences(tokens, setMark+1, setEnd); err != nil {
		return nil, err
	}

	var predicateSpans []span
	if fromMark, ok := marks["from"]; ok {
		fromEnd := nextClauseAfter(marks, union, fromMark, end)
		fromRefs, spans, err := g.parseFrom(tokens, fromMark+1, fromEnd, tokens[fromMark].depth, ctes)
		if err != nil {
			return nil, err
		}
		refs = append(refs, fromRefs...)
		predicateSpans = append(predicateSpans, spans...)
	}
	if whereMark, ok := marks["where"]; ok {
		whereEnd := nextClauseAfter(marks, union, whereMark, end)
		if err := g.rejectTenantReferences(tokens, whereMark+1, whereEnd); err != nil {
			return nil, err
		}
	}
	for _, predicate := range predicateSpans {
		if err := g.rejectTenantReferences(tokens, predicate.start, predicate.end); err != nil {
			return nil, err
		}
	}

	return g.buildDMLPredicateEdit(tokens, marks, union, end, refs)
}

func (g *Guard) guardDelete(tokens []token, start, end int, ctes []cteDefinition) ([]edit, error) {
	deleteIndex := nextSignificant(tokens, start, end)
	fromIndex := nextSignificant(tokens, deleteIndex+1, end)
	if fromIndex >= end || !isWord(tokens[fromIndex], "from") {
		return nil, fmt.Errorf("%w: DELETE without FROM", ErrMalformedQuery)
	}
	i := nextSignificant(tokens, fromIndex+1, end)
	if i < end && isWord(tokens[i], "only") {
		i = nextSignificant(tokens, i+1, end)
	}
	if i >= end || !isIdent(tokens[i]) {
		return nil, fmt.Errorf("%w: expected DELETE target table", ErrMalformedQuery)
	}
	targetIndex := i
	parts, next := readQualifiedName(tokens, i, end)
	alias, next := parseAlias(tokens, next, end)
	if alias == "" {
		alias = parts[len(parts)-1].text
	}
	target, ok, err := g.referenceFor(parts, alias, targetIndex, ctes)
	if err != nil {
		return nil, err
	}
	firstClause := nextSignificant(tokens, next, end)
	if firstClause < end {
		switch {
		case isWord(tokens[firstClause], "using"),
			isWord(tokens[firstClause], "where"),
			isWord(tokens[firstClause], "returning"),
			isWord(tokens[firstClause], "order"),
			isWord(tokens[firstClause], "limit"):
		default:
			return nil, fmt.Errorf("%w: DELETE with a multi-table target list is not supported", ErrUnsupportedQuery)
		}
	}
	refs := []tableReference{}
	if ok {
		refs = append(refs, target)
	}

	marks, union := clauseMarks(tokens, next, end, tokens[deleteIndex].depth)
	var predicateSpans []span
	if usingMark, ok := marks["using"]; ok {
		usingEnd := nextClauseAfter(marks, union, usingMark, end)
		usingRefs, spans, err := g.parseFrom(tokens, usingMark+1, usingEnd, tokens[usingMark].depth, ctes)
		if err != nil {
			return nil, err
		}
		refs = append(refs, usingRefs...)
		predicateSpans = append(predicateSpans, spans...)
	}
	if whereMark, ok := marks["where"]; ok {
		whereEnd := nextClauseAfter(marks, union, whereMark, end)
		if err := g.rejectTenantReferences(tokens, whereMark+1, whereEnd); err != nil {
			return nil, err
		}
	}
	for _, predicate := range predicateSpans {
		if err := g.rejectTenantReferences(tokens, predicate.start, predicate.end); err != nil {
			return nil, err
		}
	}
	return g.buildDMLPredicateEdit(tokens, marks, union, end, refs)
}

func clauseMarks(tokens []token, start, end, depth int) (map[string]int, int) {
	marks := make(map[string]int)
	union := end
	for i := start; i < end; i++ {
		if tokens[i].depth != depth || tokens[i].kind != tokWord {
			continue
		}
		switch tokens[i].lower {
		case "union", "except", "intersect":
			union = i
			i = end
		case "set", "from", "using", "where", "having", "qualify", "returning",
			"group", "window", "order", "limit", "offset", "fetch", "for", "option":
			if _, exists := marks[tokens[i].lower]; !exists {
				marks[tokens[i].lower] = i
			}
		}
	}
	return marks, union
}

func nextClauseAfter(marks map[string]int, union, after, end int) int {
	next := union
	for _, mark := range marks {
		if mark > after && mark < next {
			next = mark
		}
	}
	if next > end {
		return end
	}
	return next
}

func (g *Guard) buildDMLPredicateEdit(tokens []token, marks map[string]int, union, end int, refs []tableReference) ([]edit, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(refs))
	predicates := make([]string, 0, len(refs))
	for _, ref := range refs {
		key := ref.qualifier + "." + ref.column
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		predicates = append(predicates, key+" = "+tenantArgumentMarker)
	}
	expression := strings.Join(predicates, " AND ")
	if whereMark, ok := marks["where"]; ok {
		whereEnd := nextClauseAfter(marks, union, whereMark, end)
		return []edit{
			{at: whereMark + 1, text: " ("},
			{at: whereEnd, text: ") AND (" + expression + ") "},
		}, nil
	}
	insertAt := union
	for _, name := range []string{"returning", "order", "limit", "offset", "fetch", "for", "option"} {
		if mark, ok := marks[name]; ok && mark < insertAt {
			insertAt = mark
		}
	}
	return []edit{{at: insertAt, text: " WHERE (" + expression + ") "}}, nil
}

func (g *Guard) referenceFor(parts []token, alias string, position int, ctes []cteDefinition) (tableReference, bool, error) {
	if g.isCTEReference(parts, position, ctes) {
		return tableReference{}, false, nil
	}
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		names = append(names, identifierKey(part))
	}
	table := strings.Join(names, ".")
	column, scoped := g.columnFor(table, names[len(names)-1])
	if !scoped {
		return tableReference{}, false, nil
	}
	return tableReference{qualifier: alias, column: column}, true, nil
}

func (g *Guard) choosePlaceholderStyle(tokens []token) (PlaceholderStyle, error) {
	if g.placeholder != PlaceholderAuto {
		return g.placeholder, nil
	}
	hasQuestion := false
	hasDollar := false
	for _, tok := range tokens {
		if tok.kind != tokPlaceholder {
			continue
		}
		if strings.HasPrefix(tok.text, "$") {
			hasDollar = true
		} else {
			hasQuestion = true
		}
	}
	if hasDollar && hasQuestion {
		return 0, fmt.Errorf("%w: mixed ? and $n placeholders", ErrArgumentMismatch)
	}
	if hasDollar {
		return PlaceholderDollar, nil
	}
	return PlaceholderQuestion, nil
}

func render(tokens []token, edits []edit, args []any, tenant any, style PlaceholderStyle) (string, []any, error) {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].at != edits[j].at {
			return edits[i].at < edits[j].at
		}
		return edits[i].seq < edits[j].seq
	})
	for i := range edits {
		edits[i].seq = i
	}

	var out strings.Builder
	out.Grow(estimateOutputSize(tokens, edits))
	finalArgs := make([]any, 0, len(args)+len(edits))
	nextPlaceholder := 1
	originalPlaceholderIndex := 0
	usedDollar := make(map[int]bool)
	editIndex := 0

	writePlaceholder := func(value any) {
		if style == PlaceholderDollar {
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(nextPlaceholder))
			nextPlaceholder++
		} else {
			out.WriteByte('?')
		}
		finalArgs = append(finalArgs, value)
	}
	writeInjected := func(text string, at int) {
		if insertionNeedsNewline(tokens, at) {
			out.WriteByte('\n')
		}
		for {
			index := strings.Index(text, tenantArgumentMarker)
			if index < 0 {
				out.WriteString(text)
				return
			}
			out.WriteString(text[:index])
			writePlaceholder(tenant)
			text = text[index+len(tenantArgumentMarker):]
		}
	}

	for i := 0; i <= len(tokens); i++ {
		for editIndex < len(edits) && edits[editIndex].at == i {
			writeInjected(edits[editIndex].text, i)
			editIndex++
		}
		if i == len(tokens) {
			break
		}
		tok := tokens[i]
		if tok.kind != tokPlaceholder {
			out.WriteString(tok.text)
			continue
		}
		switch style {
		case PlaceholderDollar:
			number, ok := placeholderNumber(tok)
			if !ok {
				return "", nil, fmt.Errorf("%w: invalid $ placeholder %q", ErrArgumentMismatch, tok.text)
			}
			if number > len(args) {
				return "", nil, fmt.Errorf("%w: placeholder %s has no argument", ErrArgumentMismatch, tok.text)
			}
			usedDollar[number] = true
			writePlaceholder(args[number-1])
		default:
			if originalPlaceholderIndex >= len(args) {
				return "", nil, fmt.Errorf("%w: too few arguments", ErrArgumentMismatch)
			}
			writePlaceholder(args[originalPlaceholderIndex])
			originalPlaceholderIndex++
		}
	}

	switch style {
	case PlaceholderDollar:
		for number := 1; number <= len(args); number++ {
			if !usedDollar[number] {
				return "", nil, fmt.Errorf("%w: argument %d is unused", ErrArgumentMismatch, number)
			}
		}
	default:
		if originalPlaceholderIndex != len(args) {
			return "", nil, fmt.Errorf("%w: too many arguments", ErrArgumentMismatch)
		}
	}
	return out.String(), finalArgs, nil
}

func insertionNeedsNewline(tokens []token, at int) bool {
	for i := at - 1; i >= 0; i-- {
		switch tokens[i].kind {
		case tokSpace:
			if strings.ContainsAny(tokens[i].text, "\r\n") {
				return false
			}
		case tokComment:
			trimmed := strings.TrimLeft(tokens[i].text, " \t")
			if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
				return true
			}
		default:
			return false
		}
	}
	return false
}

func estimateOutputSize(tokens []token, edits []edit) int {
	size := 0
	for _, tok := range tokens {
		size += len(tok.text)
	}
	for _, e := range edits {
		size += len(e.text) + 8
	}
	return size + 32
}
