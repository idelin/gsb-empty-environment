package mvrewrite

import "fmt"

// AmbiguityWitness is one side of a null-discrimination proof.
type AmbiguityWitness struct {
	Label       string           `json:"label"`
	Database    *Database        `json:"database"`
	QueryRows   []map[string]any `json:"query_rows,omitempty"`
	ViewRows    []map[string]any `json:"view_rows,omitempty"`
	QueryKeys   []string         `json:"query_keys,omitempty"`
	ViewKeys    []string         `json:"view_keys,omitempty"`
	Description string           `json:"description"`
}

// AmbiguityProof demonstrates why no filter over exported view columns can
// rewrite the query: padded and real-NULL databases yield the same exposed
// view relation but different query results.
type AmbiguityProof struct {
	Explanation string            `json:"explanation"`
	Target      ColRef            `json:"target"`
	Padded      *AmbiguityWitness `json:"padded"`
	TrueNull    *AmbiguityWitness `json:"true_null"`
}

func buildAmbiguityProof(catalog *Catalog, q *Query, v *View) *AmbiguityProof {
	targets := ambiguityTargets(catalog, q, v)
	for _, target := range targets {
		proof := constructAmbiguityProof(catalog, q, v, target)
		if proof != nil {
			return proof
		}
	}
	return nil
}

func ambiguityTargets(catalog *Catalog, q *Query, v *View) []ColRef {
	output := map[ColRef]struct{}{}
	for _, c := range v.Select {
		output[c] = struct{}{}
	}
	var targets []ColRef
	seen := map[ColRef]bool{}
	add := func(c ColRef) {
		if c.Table == "" || seen[c] {
			return
		}
		seen[c] = true
		targets = append(targets, c)
	}

	for _, p := range allQueryPredicates(q) {
		if p.Op != OpNotNull && p.Op != OpIsNull {
			continue
		}
		if _, ok := output[p.Col]; ok {
			continue
		}
		if sub := substitutePaddingPredicate(catalog, v, p); sub != nil {
			continue
		}
		add(p.Col)
	}

	for _, g := range collectPredicateGroups(q) {
		if g.top || g.owner == nil {
			continue
		}
		if _, err := ownerDiscriminator(catalog, v.Select, g.owner); err != nil {
			scan := leftmostScan(g.owner)
			if scan != nil {
				alias := scan.Alias
				if alias == "" {
					alias = scan.Table
				}
				add(C(alias, ""))
			}
		}
	}
	return targets
}

func allQueryPredicates(q *Query) []Predicate {
	var out []Predicate
	out = append(out, effectivePredicates(q.Where, q.NullReject)...)
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		out = append(out, effectivePredicates(n.Where, n.NullReject)...)
		walk(n.Left)
		walk(n.Right)
	}
	walk(q.Root)
	return out
}

func constructAmbiguityProof(catalog *Catalog, q *Query, v *View, target ColRef) *AmbiguityProof {
	alias := target.Table
	physical := alias
	if catalog != nil {
		if _, ok := catalog.Tables[physical]; !ok {
			for _, s := range collectScans(q.Root) {
				if s.Alias == alias {
					physical = s.Physical
					break
				}
			}
		}
	}

	paddedDB := zeroDatabase(catalog, q, v, physical, true)
	realDB := zeroDatabase(catalog, q, v, physical, false)

	paddedQ, paddedV, err := evalProofSides(catalog, q, v, paddedDB)
	if err != nil {
		return nil
	}
	realQ, realV, err := evalProofSides(catalog, q, v, realDB)
	if err != nil {
		return nil
	}
	if sameStringSet(paddedV.Keys(), realV.Keys()) && !sameStringSet(paddedQ.Keys(), realQ.Keys()) {
		return &AmbiguityProof{
			Target:      target,
			Explanation: "a padded outer-join row and a real base NULL produce identical exported view tuples; no filter on exported columns can separate them",
			Padded: &AmbiguityWitness{
				Label:       "padded",
				Description: fmt.Sprintf("table %s is empty, so the outer join pads its subtree with NULLs", physical),
				Database:    paddedDB,
				QueryRows:   resultPublicRows(paddedQ),
				ViewRows:    resultPublicRows(paddedV),
				QueryKeys:   paddedQ.Keys(),
				ViewKeys:    paddedV.Keys(),
			},
			TrueNull: &AmbiguityWitness{
				Label:       "real-null",
				Description: fmt.Sprintf("table %s has one matching row whose unconstrained nullable column is NULL", physical),
				Database:    realDB,
				QueryRows:   resultPublicRows(realQ),
				ViewRows:    resultPublicRows(realV),
				QueryKeys:   realQ.Keys(),
				ViewKeys:    realV.Keys(),
			},
		}
	}
	return nil
}

func evalProofSides(catalog *Catalog, q *Query, v *View, db *Database) (*ResultSet, *ResultSet, error) {
	evalQ := q
	if q.Schema == nil && catalog != nil {
		qc := *q
		qc.Schema = catalog
		evalQ = &qc
	}
	qr, err := EvalQuery(evalQ, db)
	if err != nil {
		return nil, nil, err
	}
	vr, err := EvalView(v, db, catalog)
	if err != nil {
		return nil, nil, err
	}
	return qr, vr, nil
}

func zeroDatabase(catalog *Catalog, q *Query, v *View, emptyPhysical string, padded bool) *Database {
	ss := buildSearchSchema(catalog, q, v, nil)
	joinCols := map[ColRef]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for _, e := range n.On {
			joinCols[e.Left] = true
			joinCols[e.Right] = true
		}
		walk(n.Left)
		walk(n.Right)
	}
	walk(q.Root)
	walk(v.Root)
	joinPhysical := map[string]bool{}
	for ref := range joinCols {
		physical := ss.aliasPhysical[ref.Table]
		if physical == "" {
			physical = ref.Table
		}
		joinPhysical[physical+"\x00"+ref.Column] = true
	}

	db := NewDatabase()
	for _, physical := range ss.physicalTables {
		if padded && physical == emptyPhysical {
			db.Tables[physical] = &TableData{Name: physical, Rows: []map[string]any{}}
			continue
		}
		row := map[string]any{}
		for _, col := range ss.columns[physical] {
			spec, hasSpec := ss.specs[physical][col]
			// In the real-NULL target table, leave ordinary nullable,
			// non-join columns NULL.  Keys and equi-join columns use zero so
			// the row matches the all-zero witness rows.
			isJoinKey := joinPhysical[physical+"\x00"+col]
			if !padded && physical == emptyPhysical && hasSpec && !spec.NotNull && !spec.PrimaryKey && !isJoinKey {
				// Ref table qualifier may be alias rather than physical; the
				// common alias==physical case is handled, and alias-aware
				// join checks below also test all aliases.
				row[col] = nil
				continue
			}
			row[col] = int64(0)
		}
		db.Tables[physical] = &TableData{Name: physical, Rows: []map[string]any{row}}
	}
	return db
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
