// Demo: run the type-mapping pipeline over a MySQL row (datetime + enum
// included), a PostgreSQL UUID and a JSON document, printing the converted
// records and the data cleaning exception log.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"typepipe/pipeline"
)

func main() {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}

	// Preset mapping rules.
	rules := []pipeline.Rule{
		{Source: pipeline.MySQL, Match: "tinyint", Target: pipeline.TypeBool},
		{Source: pipeline.MySQL, Match: "datetime", Target: pipeline.TypeISO8601, Location: shanghai},
		{Source: pipeline.MySQL, Match: "enum", Target: pipeline.TypeDictCode,
			Dict: map[string]int{"small": 1, "medium": 2, "large": 3}},
		{Source: pipeline.MySQL, Match: "smallint", Target: pipeline.TypeInt,
			Min: -128, Max: 127, Overflow: pipeline.Clamp},
		{Source: pipeline.MySQL, Match: "char", Target: pipeline.TypeUUID},
		{Source: pipeline.Postgres, Match: "uuid", Target: pipeline.TypeString},
		{Source: pipeline.JSONDoc, Match: "number", Target: pipeline.TypeInt,
			Min: 0, Max: 255, Overflow: pipeline.UseDefault},
	}

	var logBuf bytes.Buffer
	engine := pipeline.New(rules, &logBuf)

	// --- MySQL record: datetime + enum + tinyint + overflow + uuid ---
	schema := []pipeline.Field{
		{Name: "id", Type: pipeline.FieldType{Name: "bigint"}},
		{Name: "is_active", Type: pipeline.FieldType{Name: "tinyint", Args: []string{"1"}}},
		{Name: "created_at", Type: pipeline.FieldType{Name: "datetime"}},
		{Name: "size", Type: pipeline.FieldType{Name: "enum", Args: []string{"small", "medium", "large"}}},
		{Name: "score", Type: pipeline.FieldType{Name: "smallint"}},
		{Name: "api_key", Type: pipeline.FieldType{Name: "char", Args: []string{"36"}}},
	}
	mysqlRow := map[string]any{
		"id":         int64(42),
		"is_active":  int64(7), // bad tinyint -> default false + warning
		"created_at": "2024-03-01 12:30:00",
		"size":       "huge",     // unknown enum label -> code 0 + warning
		"score":      int64(300), // overflows [-128,127] -> clamped
		"api_key":    "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11",
	}
	printJSON("MySQL -> canonical", engine.ConvertRecord(pipeline.MySQL, schema, mysqlRow))

	// --- PostgreSQL uuid -> string ---
	pgSchema := []pipeline.Field{{Name: "id", Type: pipeline.FieldType{Name: "uuid"}}}
	pgRow := map[string]any{"id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}
	printJSON("PostgreSQL -> canonical", engine.ConvertRecord(pipeline.Postgres, pgSchema, pgRow))

	// --- JSON document: numeric overflow with UseDefault policy ---
	jsonSchema := []pipeline.Field{{Name: "port", Type: pipeline.FieldType{Name: "number"}}}
	jsonRow := map[string]any{"port": json.Number("70000")} // > 255 -> default 0
	printJSON("JSONDoc -> canonical", engine.ConvertRecord(pipeline.JSONDoc, jsonSchema, jsonRow))

	// --- Data cleaning exception log ---
	fmt.Println("=== data cleaning exception log ===")
	os.Stdout.Write(logBuf.Bytes())
}

func printJSON(title string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("=== %s ===\n%s\n\n", title, b)
}
