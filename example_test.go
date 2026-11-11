package jsonpathstream_test

import (
	"fmt"
	"strings"

	jps "example.org/jsonpathstream"
)

func ExampleEvaluateReader() {
	query := "$.store.book[?(@.price < 10)].title"
	p, err := jps.Parse(query)
	if err != nil {
		panic(err)
	}
	doc := strings.NewReader(`{"store":{"book":[` +
		`{"title":"a","price":20},` +
		`{"title":"b","price":5}]}}`)
	err = jps.EvaluateReader(p, doc, func(m jps.Match) error {
		fmt.Println(m.Path, "=", m.Value)
		return nil
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// $.store.book[1].title = b
}
