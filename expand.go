package taskpkg

import (
	"fmt"
	"sort"
	"strings"
)

type nodeDraft struct {
	id         string
	types      []string
	properties map[string][]Value
	sources    []Source
}

// Build expands every script block, merges nodes by their expanded @id, and
// then extracts the article/author view.
func (b *Builder) Build() (*Graph, error) {
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	b.unresolvedCtx = nil
	b.pageURL = strings.TrimSpace(b.pageURL)
	b.articleTypes = normalizeTypeIRIs(b.articleTypes, []string{
		"https://schema.org/Article",
	})
	b.personTypes = normalizeTypeIRIs(b.personTypes, []string{
		"https://schema.org/Person",
	})
	b.single = normalizeSingleValued(b.single)

	nodes := make(map[string]*Node)
	b.currentNodes = nodes
	for index, script := range b.scripts {
		if script.tree == nil {
			continue
		}
		ctx := newContext(b.pageURL)
		b.expandTop(script.tree, ctx, index, "", nodes)
	}
	b.finalizeNodes(nodes)

	graph := &Graph{
		Nodes:    nodes,
		Articles: []Article{},
		Authors:  []Author{},
		Edges:    []Edge{},
	}
	graph.Scripts = make([]ScriptInfo, 0, len(b.scripts))
	for i, script := range b.scripts {
		graph.Scripts = append(graph.Scripts, ScriptInfo{Script: i, Hash: script.hash})
	}
	graph.UnresolvedContexts = append(graph.UnresolvedContexts, b.unresolvedCtx...)
	sort.SliceStable(graph.UnresolvedContexts, func(i, j int) bool {
		if graph.UnresolvedContexts[i].Script != graph.UnresolvedContexts[j].Script {
			return graph.UnresolvedContexts[i].Script < graph.UnresolvedContexts[j].Script
		}
		if graph.UnresolvedContexts[i].Path != graph.UnresolvedContexts[j].Path {
			return graph.UnresolvedContexts[i].Path < graph.UnresolvedContexts[j].Path
		}
		return graph.UnresolvedContexts[i].Context < graph.UnresolvedContexts[j].Context
	})
	b.extract(nodes, graph)
	return graph, nil
}

func (b *Builder) expandTop(value any, ctx *activeContext, script int, path string, nodes map[string]*Node) {
	switch typed := value.(type) {
	case []any:
		for i, item := range typed {
			b.expandTop(item, ctx, script, pointerPath(path, fmt.Sprintf("%d", i)), nodes)
		}
	case map[string]any:
		localCtx := ctx
		if rawContext, ok := typed["@context"]; ok {
			var unresolved []UnresolvedContext
			localCtx, unresolved = ctx.processContext(rawContext, script, pointerPath(path, "@context"))
			b.unresolvedCtx = append(b.unresolvedCtx, unresolved...)
		}

		graphValue, hasGraph := keywordLookup(typed, localCtx, "@graph")
		if hasGraph {
			b.expandGraphValue(graphValue, localCtx, script, pointerPath(path, "@graph"), nodes)
			hasNodeContent := false
			for key := range typed {
				expanded := localCtx.expandIRI(key, true, false)
				if expanded == "@context" || expanded == "@graph" {
					continue
				}
				hasNodeContent = true
				break
			}
			if !hasNodeContent {
				return
			}
		}
		b.nodeFromObject(typed, localCtx, script, path, true, nodes)
	default:
		// A JSON-LD document consisting solely of a scalar has no graph node.
	}
}

func (b *Builder) expandGraphValue(value any, ctx *activeContext, script int, path string, nodes map[string]*Node) {
	switch typed := value.(type) {
	case []any:
		for i, item := range typed {
			b.expandTop(item, ctx, script, pointerPath(path, fmt.Sprintf("%d", i)), nodes)
		}
	case map[string]any:
		b.expandTop(typed, ctx, script, path, nodes)
	}
}

func (b *Builder) nodeFromObject(obj map[string]any, ctx *activeContext, script int, path string, forceAdd bool, nodes map[string]*Node) string {
	var id string
	explicitID := false
	if rawID, ok := keywordLookup(obj, ctx, "@id"); ok {
		if idValue, ok := rawID.(string); ok && idValue != "" {
			id = ctx.expandIRI(idValue, false, true)
			explicitID = true
		}
	}

	var types []string
	if rawType, ok := keywordLookup(obj, ctx, "@type"); ok {
		for _, item := range anySlice(rawType) {
			typeValue, ok := item.(string)
			if !ok || typeValue == "" {
				continue
			}
			expanded := ctx.expandIRI(typeValue, true, false)
			types = appendUnique(types, expanded)
		}
	}

	properties := make(map[string][]Value)
	sources := []Source{{Script: script, Path: path}}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expandedKey := ctx.expandIRI(key, true, false)
		switch expandedKey {
		case "@context", "@id", "@type":
			continue
		case "@graph":
			// Top-level @graph is expanded by expandTop. Nested graph
			// containers are not needed for the article/author walk.
			continue
		case "@included":
			if included, ok := keywordLookup(obj, ctx, "@included"); ok {
				b.expandGraphValue(included, ctx, script, pointerPath(path, "@included"), nodes)
			}
			continue
		case "@index", "@reverse":
			continue
		}
		if expandedKey == "" || isKeyword(expandedKey) {
			continue
		}
		if !isAbsoluteIRI(expandedKey) && !strings.HasPrefix(expandedKey, "_:") {
			// Unmapped terms are dropped, as in JSON-LD expansion.
			continue
		}
		def, _ := ctx.term(key)
		valueCtx := ctx
		valuePath := pointerPath(path, expandedKey)
		if def != nil && def.contextValue != nil {
			var unresolved []UnresolvedContext
			valueCtx, unresolved = ctx.processContext(def.contextValue, script, pointerPath(valuePath, "@context"))
			b.unresolvedCtx = append(b.unresolvedCtx, unresolved...)
		}

		values := b.expandProperty(obj[key], valueCtx, def, script, valuePath)
		if def != nil && def.container == "@list" {
			if !containsListValue(values) {
				items := append([]Value(nil), values...)
				values = []Value{{
					Kind:    ValueKindList,
					List:    items,
					Sources: []Source{{Script: script, Path: pointerPath(path, key)}},
				}}
			}
		}
		for _, value := range values {
			properties[expandedKey] = mergeValue(properties[expandedKey], value)
		}
	}

	if id == "" {
		draft := &nodeDraft{types: types, properties: properties}
		id = "_:b" + sha256Text(draft.contentKey())[:32]
	}
	draft := &nodeDraft{
		id:         id,
		types:      types,
		properties: properties,
		sources:    sources,
	}
	if explicitID || len(types) > 0 || len(properties) > 0 {
		b.mergeDraft(draft, nodes)
	}
	return id
}

func (b *Builder) expandProperty(raw any, ctx *activeContext, def *termDefinition, script int, path string) []Value {
	switch typed := raw.(type) {
	case []any:
		var out []Value
		for i, item := range typed {
			out = append(out, b.expandPropertyItem(item, ctx, def, script, pointerPath(path, fmt.Sprintf("%d", i)))...)
		}
		return out
	default:
		return b.expandPropertyItem(raw, ctx, def, script, path)
	}
}

func (b *Builder) expandPropertyItem(raw any, ctx *activeContext, def *termDefinition, script int, path string) []Value {
	if raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]any:
		localCtx := ctx
		if rawContext, ok := typed["@context"]; ok {
			var unresolved []UnresolvedContext
			localCtx, unresolved = ctx.processContext(rawContext, script, pointerPath(path, "@context"))
			b.unresolvedCtx = append(b.unresolvedCtx, unresolved...)
		}

		if value, ok := keywordLookup(typed, localCtx, "@value"); ok {
			return []Value{b.literalFromValueObject(value, typed, localCtx, def, script, path)}
		}
		if listValue, ok := keywordLookup(typed, localCtx, "@list"); ok {
			items := b.expandListItems(listValue, localCtx, def, script, pointerPath(path, "@list"))
			return []Value{{
				Kind:    ValueKindList,
				List:    items,
				Sources: []Source{{Script: script, Path: path}},
			}}
		}
		if setValue, ok := keywordLookup(typed, localCtx, "@set"); ok {
			return b.expandProperty(setValue, localCtx, def, script, path)
		}

		if rawID, hasID := keywordLookup(typed, localCtx, "@id"); hasID {
			idValue, ok := rawID.(string)
			if !ok || idValue == "" {
				return nil
			}
			id := localCtx.expandIRI(idValue, def != nil && def.typeMapping == "@vocab", true)
			hasContent := false
			for key := range typed {
				expanded := localCtx.expandIRI(key, true, false)
				if expanded != "@context" && expanded != "@id" {
					hasContent = true
					break
				}
			}
			if hasContent {
				b.nodeFromObject(typed, localCtx, script, path, false, b.currentNodes)
			}
			return []Value{{
				Kind:    ValueKindIRI,
				IRI:     id,
				Sources: []Source{{Script: script, Path: path}},
			}}
		}

		id := b.nodeFromObject(typed, localCtx, script, path, false, b.currentNodes)
		if id == "" {
			return nil
		}
		return []Value{{
			Kind:    ValueKindIRI,
			IRI:     id,
			Sources: []Source{{Script: script, Path: path}},
		}}
	case string:
		return []Value{b.literalOrReference(typed, ctx, def, script, path)}
	case bool, float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, jsonNumber:
		return []Value{{
			Kind:    ValueKindLiteral,
			Literal: typed,
			Sources: []Source{{Script: script, Path: path}},
		}}
	default:
		return []Value{{
			Kind:    ValueKindLiteral,
			Literal: raw,
			Sources: []Source{{Script: script, Path: path}},
		}}
	}
}

func (b *Builder) literalOrReference(value string, ctx *activeContext, def *termDefinition, script int, path string) Value {
	source := []Source{{Script: script, Path: path}}
	datatype := ""
	if def != nil {
		switch def.typeMapping {
		case "@id":
			return Value{Kind: ValueKindIRI, IRI: ctx.expandIRI(value, false, true), Sources: source}
		case "@vocab":
			return Value{Kind: ValueKindIRI, IRI: ctx.expandIRI(value, true, false), Sources: source}
		case "":
		default:
			if !isKeyword(def.typeMapping) {
				datatype = ctx.expandIRI(def.typeMapping, true, false)
			}
		}
	}
	language := ""
	if def != nil {
		language = def.language
	}
	if language == "" {
		language = ctx.language
	}
	return Value{Kind: ValueKindLiteral, Literal: value, Type: datatype, Language: language, Sources: source}
}

func (b *Builder) literalFromValueObject(value any, obj map[string]any, ctx *activeContext, def *termDefinition, script int, path string) Value {
	out := Value{Kind: ValueKindLiteral, Literal: value, Sources: []Source{{Script: script, Path: path}}}
	if rawType, ok := keywordLookup(obj, ctx, "@type"); ok {
		if typeValue, ok := rawType.(string); ok {
			out.Type = ctx.expandIRI(typeValue, true, false)
		}
	}
	if rawLanguage, ok := keywordLookup(obj, ctx, "@language"); ok {
		if language, ok := rawLanguage.(string); ok {
			out.Language = language
		}
	} else if _, isString := value.(string); isString {
		out.Language = ctx.language
		if def != nil && def.language != "" {
			out.Language = def.language
		}
	}
	return out
}

func (b *Builder) expandListItems(raw any, ctx *activeContext, def *termDefinition, script int, path string) []Value {
	var out []Value
	items := anySlice(raw)
	// A list is a collection, so a literal array is not silently converted
	// into multiple lists: each member is one ordered list item.
	if _, isArray := raw.([]any); !isArray {
		items = []any{raw}
	}
	for i, item := range items {
		out = append(out, b.expandPropertyItem(item, ctx, def, script, pointerPath(path, fmt.Sprintf("%d", i)))...)
	}
	return out
}

func (b *Builder) mergeDraft(d *nodeDraft, nodes map[string]*Node) {
	if d.id == "" {
		return
	}
	node, ok := nodes[d.id]
	if !ok {
		node = &Node{
			ID:         d.id,
			Types:      []string{},
			Properties: make(map[string][]Value),
		}
		nodes[d.id] = node
	}
	for _, typ := range d.types {
		node.Types = appendUnique(node.Types, typ)
	}
	node.Sources = mergeSources(node.Sources, d.sources...)
	for property, values := range d.properties {
		for _, value := range values {
			node.Properties[property] = mergeValue(node.Properties[property], value)
		}
	}
}

func (b *Builder) finalizeNodes(nodes map[string]*Node) {
	for _, node := range nodes {
		node.Types = sortedTypes(node.Types)
		sortSources(node.Sources)
		node.Fields = make(map[string]FieldState)
		for property, values := range node.Properties {
			sortValues(values)
			for i := range values {
				sortSources(values[i].Sources)
				sortValueItemSources(values[i].List)
			}
			node.Properties[property] = values
		}

		var conflicts []Conflict
		declaredSeen := make(map[string]struct{})
		for _, declared := range b.single {
			for _, property := range matchingProperties(node, declared) {
				if _, ok := declaredSeen[property]; ok {
					continue
				}
				declaredSeen[property] = struct{}{}
				values := node.Properties[property]
				if len(values) == 0 {
					continue
				}
				field := FieldState{Property: property, Present: true}
				if len(values) == 1 {
					v := values[0]
					field.Value = &v
					node.Fields[property] = field
					continue
				}
				field.Conflict = true
				field.Values = append([]Value(nil), values...)
				node.Fields[property] = field
				conflicts = append(conflicts, Conflict{
					Property: property,
					Values:   append([]Value(nil), values...),
				})
			}
		}
		sort.SliceStable(conflicts, func(i, j int) bool {
			return conflicts[i].Property < conflicts[j].Property
		})
		node.Conflicts = conflicts
		if len(node.Fields) == 0 {
			node.Fields = nil
		}
	}
}

func (d *nodeDraft) contentKey() string {
	var b strings.Builder
	types := sortedTypes(d.types)
	b.WriteString("types:")
	for _, typ := range types {
		b.WriteString(typ)
		b.WriteByte('|')
	}
	keys := make([]string, 0, len(d.properties))
	for key := range d.properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		values := append([]Value(nil), d.properties[key]...)
		sortValues(values)
		for _, value := range values {
			b.WriteString(value.Key())
			b.WriteByte(';')
		}
		b.WriteByte('|')
	}
	return b.String()
}

func keywordLookup(obj map[string]any, ctx *activeContext, keyword string) (any, bool) {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expanded := key
		if key != keyword {
			expanded = ctx.expandIRI(key, true, false)
		}
		if expanded == keyword {
			return obj[key], true
		}
	}
	return nil, false
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	default:
		return []any{value}
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeValue(values []Value, value Value) []Value {
	if value.Kind == ValueKindList {
		// Lists are ordered values; identical occurrences are not collapsed.
		return append(values, value)
	}
	key := value.Key()
	for i := range values {
		if values[i].Key() != key {
			continue
		}
		values[i].Sources = mergeSources(values[i].Sources, value.Sources...)
		return values
	}
	return append(values, value)
}

func mergeValueSequence(dst, src []Value) []Value {
	for _, value := range src {
		dst = mergeValue(dst, value)
	}
	return dst
}

func containsListValue(values []Value) bool {
	for _, value := range values {
		if value.Kind == ValueKindList {
			return true
		}
	}
	return false
}

func sortValues(values []Value) {
	sort.SliceStable(values, func(i, j int) bool {
		return values[i].Key() < values[j].Key()
	})
}

func sortValueItemSources(values []Value) {
	for i := range values {
		sortSources(values[i].Sources)
		if values[i].Kind == ValueKindList {
			sortValueItemSources(values[i].List)
		}
	}
}

func pointerPath(base string, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return base + "/" + token
}

func matchingProperties(node *Node, declared string) []string {
	if _, ok := node.Properties[declared]; ok {
		return []string{declared}
	}
	if isAbsoluteIRI(declared) {
		return nil
	}
	if strings.HasPrefix(declared, "schema:") {
		rest := strings.TrimPrefix(declared, "schema:")
		for _, candidate := range []string{"https://schema.org/" + rest, "http://schema.org/" + rest} {
			if _, ok := node.Properties[candidate]; ok {
				return []string{candidate}
			}
		}
	}
	local := localName(declared)
	var matches []string
	for property := range node.Properties {
		if localName(property) == local {
			matches = append(matches, property)
		}
	}
	sort.Strings(matches)
	return matches
}

func localName(iri string) string {
	if idx := strings.LastIndexAny(iri, "/#"); idx >= 0 && idx < len(iri)-1 {
		return iri[idx+1:]
	}
	return iri
}

// jsonNumber is an alias used only to route json.Number through the literal
// case without importing encoding/json in this file's type switch.
type jsonNumber = interface{ String() string }
