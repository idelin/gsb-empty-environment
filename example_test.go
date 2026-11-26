package mvrewrite

import "fmt"

func ExampleAnalyze() {
	catalog := NewCatalog().
		Table("a", PKCol("id")).
		Table("b", PKCol("id"), NotNullCol("aid"), Col("note"))

	q := &Query{
		Schema:     catalog,
		Root:       Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		NullReject: []ColRef{C("b", "id")},
		Select:     []ColRef{C("a", "id"), C("b", "id")},
	}
	v := &View{
		ID:     "left-join-view",
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}

	report, err := Analyze(catalog, q, []*View{v})
	if err != nil {
		panic(err)
	}
	fmt.Println(report.IDs())
	fmt.Println(report.Decisions[0].Compensation.Filter.SQL())
	// Output:
	// [left-join-view]
	// (b.id IS NOT NULL)
}
