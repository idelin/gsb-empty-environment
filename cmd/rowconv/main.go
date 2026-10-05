// Command rowconv converts a batch of JSON rows from a source schema to a
// target schema. It reads a request document from stdin (or a file given
// as the first argument) and writes per-row results to stdout. No external
// database is involved.
//
// Request format:
//
//	{
//	  "sourceSchema": {"fields": [{"name": "...", "type": "int|decimal|string|bytes|timestamp", ...}]},
//	  "targetSchema": {"fields": [...]},
//	  "mapping":  {"sourceName": "targetName"},
//	  "options":  {"rounding": "reject" | "ties-to-even"},
//	  "rows":     [{...}, ...]
//	}
//
// Row values: integers as JSON numbers; decimals as
// {"coefficient": "<int>", "scale": n}; strings as JSON strings; bytes as
// base64 strings; timestamps as RFC3339 strings with explicit offset.
// Output timestamps are integer UTC microseconds.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"rowconv"
)

type request struct {
	Source  rowconv.Schema               `json:"sourceSchema"`
	Target  rowconv.Schema               `json:"targetSchema"`
	Mapping map[string]string            `json:"mapping"`
	Options rowconv.Options              `json:"options"`
	Rows    []map[string]json.RawMessage `json:"rows"`
}

type rowResult struct {
	Index int         `json:"index"`
	OK    bool        `json:"ok"`
	Row   rowconv.Row `json:"row,omitempty"`
	Error string      `json:"error,omitempty"`
}

type response struct {
	Results []rowResult `json:"results"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rowconv:", err)
		os.Exit(2)
	}
}

func run() error {
	var in io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("invalid request JSON: %v", err)
	}
	conv, err := rowconv.NewConverter(&req.Source, &req.Target, req.Mapping, req.Options)
	if err != nil {
		return err
	}

	resp := response{Results: make([]rowResult, 0, len(req.Rows))}
	for i, rawRow := range req.Rows {
		res := rowResult{Index: i}
		row, err := rowconv.DecodeRow(&req.Source, rawRow, req.Options)
		if err == nil {
			var out rowconv.Row
			out, err = conv.ConvertRow(row)
			res.Row = out
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
		}
		resp.Results = append(resp.Results, res)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}
