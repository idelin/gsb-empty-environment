// rtreecli is a line-delimited JSON front-end for the R-tree.
//
// Usage: rtreecli -capacity 8 < commands.jsonl
//
// Input, one JSON object per line:
//   {"op":"insert","id":1,"rect":[x1,y1,x2,y2]}
//   {"op":"replace","id":1,"rect":[x1,y1,x2,y2]}
//   {"op":"delete","id":1}
//   {"op":"query","rect":[x1,y1,x2,y2]}
//   {"op":"validate"} | {"op":"size"} | {"op":"height"}
//
// Output, one JSON object per command:
//   {"ok":true}                        for mutations
//   {"ok":true,"ids":[...],"visited":N} for query
//   {"ok":true,"size":N} / {"ok":true,"height":N}
//   {"ok":false,"error":"..."}         on failure
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"rtree"
)

type command struct {
	Op   string   `json:"op"`
	ID   int64    `json:"id"`
	Rect [4]int64 `json:"rect"`
}

func reply(ok bool, kv ...interface{}) {
	m := map[string]interface{}{"ok": ok}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	out, _ := json.Marshal(m)
	fmt.Println(string(out))
}

func main() {
	capacity := flag.Int("capacity", 8, "maximum entries per tree node (>= 4)")
	flag.Parse()
	tr, err := rtree.New(*capacity)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var c command
		if err := json.Unmarshal(line, &c); err != nil {
			reply(false, "error", "bad json: "+err.Error())
			continue
		}
		switch c.Op {
		case "insert":
			if err := tr.Insert(c.ID, rtree.NewRect(c.Rect[0], c.Rect[1], c.Rect[2], c.Rect[3])); err != nil {
				reply(false, "error", err.Error())
			} else {
				reply(true)
			}
		case "replace":
			if err := tr.Replace(c.ID, rtree.NewRect(c.Rect[0], c.Rect[1], c.Rect[2], c.Rect[3])); err != nil {
				reply(false, "error", err.Error())
			} else {
				reply(true)
			}
		case "delete":
			if err := tr.Delete(c.ID); err != nil {
				reply(false, "error", err.Error())
			} else {
				reply(true)
			}
		case "query":
			ids, visited := tr.RangeQuery(rtree.NewRect(c.Rect[0], c.Rect[1], c.Rect[2], c.Rect[3]))
			reply(true, "ids", ids, "visited", visited)
		case "validate":
			if err := tr.Validate(); err != nil {
				reply(false, "error", err.Error())
			} else {
				reply(true)
			}
		case "size":
			reply(true, "size", tr.Size())
		case "height":
			reply(true, "height", tr.Height())
		default:
			reply(false, "error", "unknown op: "+c.Op)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
