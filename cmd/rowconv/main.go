// Command rowconv reads a JSON conversion job from stdin and writes per-row
// results as JSON to stdout. It never touches an external database.
//
// Input:
//
//	{
//	  "source_schema": {"fields": [...]},
//	  "target_schema": {"fields": [...]},
//	  "mapping": {"target_field": "source_field"},
//	  "config": {"decimal_mode": "reject|half_even",
//	             "timestamp_mode": "reject|half_even"},
//	  "rows": [ {...}, ... ]
//	}
//
// Value encoding in rows: JSON null = explicit null; absent key = missing;
// numbers = int; strings = string; {"$bytes": "<base64>"};
// {"$decimal": {"coef": "<int64>", "scale": n}}; {"$ts": "<RFC3339+offset>"}.
// Converted timestamps come out as {"$ts_micros": <int64 UTC micros>}.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"rowconv"
)

type request struct {
	Source  rowconv.Schema             `json:"source_schema"`
	Target  rowconv.Schema             `json:"target_schema"`
	Mapping map[string]string          `json:"mapping"`
	Config  rowconv.Config             `json:"config"`
	Rows    []map[string]rowconv.Value `json:"rows"`
}

type response struct {
	Results []rowconv.Result `json:"results"`
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "rowconv:", err)
	os.Exit(1)
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fatal(fmt.Errorf("decoding input: %v", err))
	}
	conv, err := rowconv.NewConverter(req.Source, req.Target, req.Mapping, req.Config)
	if err != nil {
		fatal(err)
	}
	resp := response{Results: conv.ConvertBatch(req.Rows)}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		fatal(err)
	}
}
