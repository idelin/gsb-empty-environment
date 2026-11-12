package taskpkg

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func q082Raw(v string) json.RawMessage { return json.RawMessage(v) }

func TestMergesArticleAndAuthorAcrossScripts(t *testing.T) {
	scripts := []json.RawMessage{
		q082Raw(`{
			"@context": {"@vocab": "https://schema.org/"},
			"@graph": [
				{"@id": "#article", "@type": "Article", "headline": "Hello",
				 "author": {"@id": "#alice"}}
			]
		}`),
		q082Raw(`{
			"@context": {"s": "https://schema.org/"},
			"@id": "#alice", "@type": "s:Person", "s:name": "Alice"
		}`),
	}
	graph, err := Build(scripts, "https://example.org/papers/1", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Articles) != 1 {
		t.Fatalf("articles = %d", len(graph.Articles))
	}
	article := graph.Articles[0]
	if article.ID != "https://example.org/papers/1#article" {
		t.Fatalf("article id = %q", article.ID)
	}
	if article.Headline != "Hello" {
		t.Fatalf("headline = %q", article.Headline)
	}
	if len(article.Authors) != 1 || article.Authors[0].Name != "Alice" {
		t.Fatalf("authors = %#v", article.Authors)
	}
	if len(article.Authors[0].Sources) != 1 || article.Authors[0].Sources[0].Script != 1 {
		t.Fatalf("author sources = %#v", article.Authors[0].Sources)
	}
}

func TestEquivalentPrefixesAndScriptOrder(t *testing.T) {
	first := q082Raw(`{
		"@context": {"s": "https://schema.org/"},
		"@graph": [
			{"@id": "#a", "@type": "s:Article", "s:author": {"@id": "#p"}, "s:headline": "T"},
			{"@id": "#p", "@type": "s:Person", "s:name": "N"}
		]
	}`)
	second := q082Raw(`{
		"@context": {"@vocab": "https://schema.org/"},
		"@graph": [
			{"@id": "#a", "@type": "Article", "author": {"@id": "#p"}, "headline": "T"},
			{"@id": "#p", "@type": "Person", "name": "N"}
		]
	}`)
	g1, err := Build([]json.RawMessage{first, second}, "https://e.test/base", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := Build([]json.RawMessage{second, first}, "https://e.test/base", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g1.Articles, g2.Articles) {
		t.Fatalf("article views differ:\n%#v\n%#v", g1.Articles, g2.Articles)
	}
	if len(g1.Nodes) != len(g2.Nodes) {
		t.Fatalf("nodes differ: %d != %d", len(g1.Nodes), len(g2.Nodes))
	}
	for id, n1 := range g1.Nodes {
		n2 := g2.Nodes[id]
		if n2 == nil {
			t.Fatalf("missing node %s", id)
		}
		if !reflect.DeepEqual(n1.Types, n2.Types) || !reflect.DeepEqual(n1.Properties, n2.Properties) {
			t.Fatalf("node %s differs:\n%#v\n%#v", id, n1, n2)
		}
	}
}

func TestNestedContextRestoresParent(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{
		"@context": {"s": "https://schema.org/"},
		"@graph": [
			{"@id": "#a", "@type": "s:Article", "s:author": [
				{"@context": {"s": "https://evil.example/", "@base": "https://evil.example/"},
				 "@id": "p", "@type": "https://schema.org/Person", "s:name": "Wrong"},
				{"@id": "#b", "@type": "s:Person", "s:name": "Right"}
			], "s:headline": "After"},
			{"@id": "#c", "@type": "s:Person", "s:name": "Sibling"}
		]
	}`)}
	g, err := Build(scripts, "https://good.example/base", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	a := g.Articles[0]
	if a.Headline != "After" {
		t.Fatalf("headline = %q", a.Headline)
	}
	if len(a.Authors) != 2 {
		t.Fatalf("authors = %#v", a.Authors)
	}
	if a.Authors[0].ID != "https://evil.example/p" || a.Authors[0].Name != "Wrong" {
		t.Fatalf("nested author = %#v", a.Authors[0])
	}
	if a.Authors[1].Name != "Right" {
		t.Fatalf("restored author = %#v", a.Authors[1])
	}
	if g.Nodes["https://good.example/base#c"].Property("https://schema.org/name")[0].Literal != "Sibling" {
		t.Fatalf("sibling context leaked")
	}
}

func TestVocabBaseAndListOrder(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{
		"@context": {
			"@vocab": "https://schema.org/",
			"@base": "https://example.org/issue/",
			"author": {"@id": "author", "@container": "@list"},
			"tags": {"@id": "keywords", "@container": "@list"}
		},
		"@id": "article-1", "@type": "Article",
		"headline": "H",
		"author": [{"@id": "p1", "@type": "Person", "name": "A"},
		           {"@id": "p2", "@type": "Person", "name": "B"}],
		"tags": ["z", "a", "z"]
	}`)}
	g, err := Build(scripts, "https://example.org/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Articles[0].ID != "https://example.org/issue/article-1" {
		t.Fatalf("id = %q", g.Articles[0].ID)
	}
	if len(g.Articles[0].Authors) != 2 || g.Articles[0].Authors[0].Name != "A" || g.Articles[0].Authors[1].Name != "B" {
		t.Fatalf("authors = %#v", g.Articles[0].Authors)
	}
	tags := g.Nodes["https://example.org/issue/article-1"].Properties["https://schema.org/keywords"]
	if len(tags) != 1 || tags[0].Kind != ValueKindList {
		t.Fatalf("tags = %#v", tags)
	}
	var got []string
	for _, item := range tags[0].List {
		if s, ok := item.StringValue(); ok {
			got = append(got, s)
		}
	}
	if !reflect.DeepEqual(got, []string{"z", "a", "z"}) {
		t.Fatalf("list order = %#v", got)
	}
}

func TestSingleValueConflictKeepsBothValuesAndSources(t *testing.T) {
	scripts := []json.RawMessage{
		q082Raw(`{"@context":{"@vocab":"https://schema.org/"},"@id":"#a","@type":"Article","headline":"First","author":{"@id":"#p"}}`),
		q082Raw(`{"@context":{"@vocab":"https://schema.org/"},"@id":"#a","@type":"Article","headline":"Second"}`),
		q082Raw(`{"@context":{"@vocab":"https://schema.org/"},"@id":"#p","@type":"Person","name":"Alice"}`),
	}
	g, err := Build(scripts, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"https://schema.org/headline", "https://schema.org/name"})
	if err != nil {
		t.Fatal(err)
	}
	a := g.Articles[0]
	if a.Headline != "" || !a.HeadlineField.Conflict {
		t.Fatalf("headline state = %#v", a.HeadlineField)
	}
	values := g.Nodes[a.ID].Properties["https://schema.org/headline"]
	if len(values) != 2 {
		t.Fatalf("headline values = %#v", values)
	}
	field := g.Nodes[a.ID].Fields["https://schema.org/headline"]
	if !field.Conflict || field.Value != nil || len(field.Values) != 2 {
		t.Fatalf("node field = %#v", field)
	}
	if got := g.Nodes[a.ID].ResolvedField("https://schema.org/headline"); got != nil {
		t.Fatalf("conflicting field returned %#v", got)
	}
	if len(values[0].Sources) != 1 || len(values[1].Sources) != 1 {
		t.Fatalf("sources = %#v", values)
	}
	if values[0].Sources[0].Script == values[1].Sources[0].Script {
		t.Fatalf("expected distinct sources: %#v", values)
	}
}

func TestCycleBecomesUnresolvedEdge(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{
		"@context": {"@vocab": "https://schema.org/"},
		"@graph": [
			{"@id": "#a", "@type": "Article", "headline": "A", "author": {"@id": "#p"}},
			{"@id": "#p", "@type": "Person", "name": "P", "author": {"@id": "#a"}}
		]
	}`)}
	g, err := Build(scripts, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.UnresolvedEdges) != 1 || g.UnresolvedEdges[0].Status != EdgeCycle {
		t.Fatalf("unresolved = %#v", g.UnresolvedEdges)
	}
	if g.UnresolvedEdges[0].From != "https://e.test/#p" || g.UnresolvedEdges[0].To != "https://e.test/#a" {
		t.Fatalf("cycle edge = %#v", g.UnresolvedEdges[0])
	}
}

func TestRemoteContextIsUnresolvedWithoutNetwork(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{"@context":"https://remote.example/context","@id":"#x","@type":"https://schema.org/Thing"}`)}
	g, err := Build(scripts, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.UnresolvedContexts) != 1 || g.UnresolvedContexts[0].Context != "https://remote.example/context" {
		t.Fatalf("unresolved contexts = %#v", g.UnresolvedContexts)
	}
}

func TestMultiValueDeduplication(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{
		"@context": {"@vocab": "https://schema.org/"},
		"@id": "#a", "@type": "Article", "headline": "H",
		"keywords": ["x", "x", "y"]
	}`)}
	g, err := Build(scripts, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"})
	if err != nil {
		t.Fatal(err)
	}
	v := g.Nodes["https://e.test/#a"].Properties["https://schema.org/keywords"]
	if len(v) != 2 {
		t.Fatalf("deduplicated values = %#v", v)
	}
}

func TestVocabCompactIRIAndExplicitList(t *testing.T) {
	scripts := []json.RawMessage{q082Raw(`{
		"@context": {
			"s": "https://schema.org/",
			"@vocab": "s:",
			"id": "@id",
			"type": "@type",
			"authors": {"@id": "author", "@container": "@list"}
		},
		"id": "#a", "type": "Article", "headline": "H",
		"authors": {"@list": [{"id": "#p1", "type": "Person", "name": "A"},
		                      {"id": "#p2", "type": "Person", "name": "B"}]}
	}`)}
	g, err := Build(scripts, "https://e.test/", []string{"Article"}, []string{"Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes["https://e.test/#a"] == nil {
		t.Fatalf("id alias/vocab not expanded: %#v", g.Nodes)
	}
	if len(g.Articles) != 1 || len(g.Articles[0].Authors) != 2 {
		t.Fatalf("articles = %#v", g.Articles)
	}
	if g.Articles[0].Authors[0].Name != "A" || g.Articles[0].Authors[1].Name != "B" {
		t.Fatalf("list author order = %#v", g.Articles[0].Authors)
	}
}

func TestAnonymousNodesAreStableAcrossScriptOrder(t *testing.T) {
	first := q082Raw(`{"@context":{"@vocab":"https://schema.org/"},"@id":"#a","@type":"Article","headline":"H","author":{"@type":"Person","name":"A"}}`)
	second := q082Raw(`{"@context":{"s":"https://schema.org/"},"@id":"#b","@type":"s:Article","s:headline":"H2","s:author":{"@type":"s:Person","s:name":"B"}}`)
	g1, err := Build([]json.RawMessage{first, second}, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := Build([]json.RawMessage{second, first}, "https://e.test/", []string{"https://schema.org/Article"}, []string{"https://schema.org/Person"}, []string{"name", "headline"})
	if err != nil {
		t.Fatal(err)
	}
	for id := range g1.Nodes {
		if strings.HasPrefix(id, "_:") && g2.Nodes[id] == nil {
			t.Fatalf("anonymous id %s is not stable", id)
		}
	}
}

func TestOptionsEntryPoint(t *testing.T) {
	graph, err := Build(Options{
		Scripts: []json.RawMessage{q082Raw(`{
			"@context": {"@vocab": "https://schema.org/"},
			"@id": "#a", "@type": "Article", "headline": "H",
			"author": {"@id": "#p", "@type": "Person", "name": "P"}
		}`)},
		PageURL:      "https://e.test/",
		ArticleTypes: []string{"Article"},
		PersonTypes:  []string{"Person"},
		SingleValued: []string{"headline", "name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Articles) != 1 || graph.Articles[0].Authors[0].Name != "P" {
		t.Fatalf("graph = %#v", graph.Articles)
	}
}

func TestSingleRawScriptInput(t *testing.T) {
	graph, err := Build(
		q082Raw(`{"@context":{"@vocab":"https://schema.org/"},"@id":"#a","@type":"Article","headline":"H"}`),
		"https://e.test/",
		[]string{"Article"},
		[]string{"Person"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Articles) != 1 || graph.Articles[0].Headline != "H" {
		t.Fatalf("articles = %#v", graph.Articles)
	}
}
