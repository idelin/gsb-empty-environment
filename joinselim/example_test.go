package joinselim_test

import (
	"fmt"

	js "prescreen.local/ps-0f39c054e3/joinselim"
)

func ExampleRewrite() {
	schema := js.NewSchema().
		AddTable(js.Table{
			Name:       "parent",
			Columns:    []js.Column{{Name: "id", Nullable: false}},
			PrimaryKey: []string{"id"},
		}).
		AddTable(js.Table{
			Name: "child",
			Columns: []js.Column{
				{Name: "id", Nullable: false},
				{Name: "parent_id", Nullable: true},
			},
			PrimaryKey: []string{"id"},
		}).
		AddForeignKey(js.ForeignKey{
			Table: "child", Columns: []string{"parent_id"},
			ReferencedTable: "parent", ReferencedColumns: []string{"id"},
		})

	query := js.Query{
		From: js.TableRef{Alias: "c", Table: "child"},
		Joins: []js.Join{{
			Type:  js.InnerJoin,
			Alias: "p",
			Table: "parent",
			On:    []js.Equality{{Left: js.Col("c", "parent_id"), Right: js.Col("p", "id")}},
		}},
		Projection: []js.Ref{js.Col("c", "id"), js.Col("p", "id")},
		Where:      []js.Predicate{js.NotNull(js.Col("c", "parent_id"))},
	}

	result, _ := js.Rewrite(query, schema)
	fmt.Println(result.RetainedAliases)
	fmt.Println(result.RemappedColumns["p.id"])
	fmt.Println(len(result.Predicates))

	// Output:
	// [c]
	// c.parent_id
	// 1
}
