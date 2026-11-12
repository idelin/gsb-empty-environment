package taskpkg

import (
	"sort"
	"strings"
)

const (
	schemaHTTPS = "https://schema.org/"
	schemaHTTP  = "http://schema.org/"
)

func (b *Builder) extract(nodes map[string]*Node, graph *Graph) {
	articleIDs := make([]string, 0, len(nodes))
	for id, node := range nodes {
		if typeAllowed(node.Types, b.articleTypes) {
			articleIDs = append(articleIDs, id)
		}
	}
	sort.Strings(articleIDs)

	var allEdges []Edge
	var allAuthors []Author
	for _, id := range articleIDs {
		node := nodes[id]
		article, edges, _ := b.extractArticle(node, nodes)
		graph.Articles = append(graph.Articles, article)
		allEdges = append(allEdges, edges...)
		allAuthors = append(allAuthors, article.Authors...)
	}

	// Reachable authors can also be extracted from Person nodes that are
	// referenced by an article; append them once to the graph-level view.
	allAuthors = dedupeAuthors(allAuthors)
	sort.SliceStable(allAuthors, func(i, j int) bool {
		if allAuthors[i].ID != allAuthors[j].ID {
			return allAuthors[i].ID < allAuthors[j].ID
		}
		return allAuthors[i].Name < allAuthors[j].Name
	})
	graph.Authors = allAuthors

	sort.SliceStable(allEdges, func(i, j int) bool {
		if allEdges[i].From != allEdges[j].From {
			return allEdges[i].From < allEdges[j].From
		}
		if allEdges[i].Property != allEdges[j].Property {
			return allEdges[i].Property < allEdges[j].Property
		}
		if allEdges[i].To != allEdges[j].To {
			return allEdges[i].To < allEdges[j].To
		}
		return allEdges[i].Status < allEdges[j].Status
	})
	graph.Edges = allEdges
	for _, edge := range allEdges {
		if edge.Status != EdgeResolved {
			graph.UnresolvedEdges = append(graph.UnresolvedEdges, edge)
		}
	}
	graph.Unresolved = append([]Edge(nil), graph.UnresolvedEdges...)
}

func (b *Builder) extractArticle(node *Node, nodes map[string]*Node) (Article, []Edge, []Author) {
	article := Article{
		ID:            node.ID,
		Types:         append([]string(nil), node.Types...),
		Sources:       append([]Source(nil), node.Sources...),
		HeadlineField: FieldState{Property: schemaHTTPS + "headline"},
		NameField:     FieldState{Property: schemaHTTPS + "name"},
	}
	if _, values := propertyValues(node, "headline"); len(values) > 0 {
		article.HeadlineField = fieldFromValues(propertyIRI(node, "headline"), values, true)
		article.Headline = fieldString(article.HeadlineField)
	}
	if _, values := propertyValues(node, "name"); len(values) > 0 {
		article.NameField = fieldFromValues(propertyIRI(node, "name"), values, true)
		article.Name = fieldString(article.NameField)
	}

	var edges []Edge
	var authors []Author
	path := map[string]bool{node.ID: true}

	var walk func(from *Node, depth int)
	walk = func(from *Node, depth int) {
		property, values := propertyValues(from, "author")
		if len(values) == 0 {
			return
		}
		if depth > 64 {
			for _, value := range values {
				edges = append(edges, Edge{
					From:     from.ID,
					Property: property,
					To:       value.IRI,
					Status:   EdgeUnresolved,
					Reason:   "author traversal depth limit reached",
					Sources:  append([]Source(nil), value.Sources...),
				})
			}
			return
		}
		for _, value := range values {
			handled := b.walkAuthorValue(from, value, property, depth, path, nodes, &edges, &authors)
			if handled {
				continue
			}
		}
	}

	walk(node, 0)
	article.Authors = dedupeAuthors(authors)
	article.AuthorRefs = edges
	return article, edges, article.Authors
}

// walkAuthorValue returns false when the value is a list that still needs to
// be traversed item by item; list members are always handled in order.
func (b *Builder) walkAuthorValue(from *Node, value Value, property string, depth int, path map[string]bool, nodes map[string]*Node, edges *[]Edge, authors *[]Author) bool {
	if value.Kind == ValueKindList {
		for _, item := range value.List {
			if item.Kind == ValueKindList {
				b.walkAuthorValue(from, item, property, depth, path, nodes, edges, authors)
				continue
			}
			b.handleAuthorValue(from, item, property, depth, path, nodes, edges, authors)
		}
		return true
	}
	b.handleAuthorValue(from, value, property, depth, path, nodes, edges, authors)
	return true
}

func (b *Builder) handleAuthorValue(from *Node, value Value, property string, depth int, path map[string]bool, nodes map[string]*Node, edges *[]Edge, authors *[]Author) {
	edge := Edge{
		From:     from.ID,
		Property: property,
		Status:   EdgeResolved,
		Sources:  append([]Source(nil), value.Sources...),
	}
	switch value.Kind {
	case ValueKindIRI:
		edge.To = value.IRI
		if value.IRI == "" {
			edge.Status = EdgeUnresolved
			edge.Reason = "empty author reference"
			*edges = append(*edges, edge)
			return
		}
		if path[value.IRI] {
			edge.Status = EdgeCycle
			edge.Reason = "reference cycle"
			*edges = append(*edges, edge)
			return
		}
		target, exists := nodes[value.IRI]
		if !exists {
			edge.Status = EdgeUnresolved
			edge.Reason = "author node not found"
			*edges = append(*edges, edge)
			return
		}
		if !typeAllowed(target.Types, b.personTypes) {
			edge.Status = EdgeUnresolved
			edge.Reason = "author node is not an allowed Person type"
			*edges = append(*edges, edge)
			return
		}
		*edges = append(*edges, edge)
		author := authorFromNode(target)
		*authors = append(*authors, author)
		path[value.IRI] = true
		property, values := propertyValues(target, "author")
		if len(values) > 0 && depth+1 > 64 {
			for _, nested := range values {
				*edges = append(*edges, Edge{
					From:     target.ID,
					Property: property,
					To:       nested.IRI,
					Status:   EdgeUnresolved,
					Reason:   "author traversal depth limit reached",
					Sources:  append([]Source(nil), nested.Sources...),
				})
			}
		} else if len(values) > 0 {
			for _, nested := range values {
				b.walkAuthorValue(target, nested, property, depth+1, path, nodes, edges, authors)
			}
		}
		delete(path, value.IRI)
	case ValueKindLiteral:
		name, ok := value.StringValue()
		if !ok {
			edge.Status = EdgeUnresolved
			edge.Reason = "author value is not a string or node reference"
			*edges = append(*edges, edge)
			return
		}
		edge.Reason = "literal author name"
		*edges = append(*edges, edge)
		*authors = append(*authors, Author{
			Name: name,
			NameField: FieldState{
				Property: schemaHTTPS + "name",
				Present:  true,
				Value:    &value,
			},
			Sources: append([]Source(nil), value.Sources...),
		})
	default:
		edge.Status = EdgeUnresolved
		edge.Reason = "unsupported author value"
		*edges = append(*edges, edge)
	}
}

func authorFromNode(node *Node) Author {
	author := Author{
		ID:        node.ID,
		Types:     append([]string(nil), node.Types...),
		Sources:   append([]Source(nil), node.Sources...),
		NameField: FieldState{Property: schemaHTTPS + "name"},
	}
	if property, values := propertyValues(node, "name"); len(values) > 0 {
		author.NameField = fieldFromValues(property, values, true)
		author.Name = fieldString(author.NameField)
	}
	return author
}

func propertyValues(node *Node, name string) (string, []Value) {
	if node == nil {
		return "", nil
	}
	for _, candidate := range []string{schemaHTTPS + name, schemaHTTP + name} {
		if values, ok := node.Properties[candidate]; ok {
			return candidate, values
		}
	}
	var candidates []string
	for property := range node.Properties {
		if localName(property) == name {
			candidates = append(candidates, property)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], node.Properties[candidates[0]]
	}
	return "", nil
}

func propertyIRI(node *Node, name string) string {
	property, _ := propertyValues(node, name)
	if property == "" {
		return schemaHTTPS + name
	}
	return property
}

func fieldFromValues(property string, values []Value, conflictIfMultiple bool) FieldState {
	field := FieldState{Property: property, Present: len(values) > 0}
	if len(values) == 1 {
		value := values[0]
		field.Value = &value
		return field
	}
	if len(values) > 1 && conflictIfMultiple {
		field.Conflict = true
		field.Values = append([]Value(nil), values...)
	}
	return field
}

func fieldString(field FieldState) string {
	if field.Value == nil || field.Conflict {
		return ""
	}
	if field.Value.Kind != ValueKindLiteral {
		return ""
	}
	value, ok := field.Value.StringValue()
	if !ok {
		return ""
	}
	return value
}

func typeAllowed(types, allowed []string) bool {
	for _, typ := range types {
		for _, permit := range allowed {
			if typ == permit || schemaEquivalent(typ, permit) {
				return true
			}
		}
	}
	return false
}

func schemaEquivalent(a, b string) bool {
	la, okA := schemaLocal(a)
	lb, okB := schemaLocal(b)
	return okA && okB && la == lb
}

func schemaLocal(value string) (string, bool) {
	for _, prefix := range []string{"https://schema.org/", "http://schema.org/"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix), true
		}
	}
	return "", false
}

func dedupeAuthors(authors []Author) []Author {
	var out []Author
	seen := make(map[string]struct{}, len(authors))
	for _, author := range authors {
		key := author.ID
		if key == "" {
			key = "<literal:" + strings.TrimSpace(author.Name) + ">"
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, author)
	}
	return out
}
