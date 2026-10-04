// Command sedt is a line-delimited JSON interface to the squared Euclidean
// distance transform store. It reads one JSON command per line on stdin and
// writes one JSON response per line on stdout.
//
// Commands:
//   {"op":"new","width":W,"height":H,"rows":["0010","0110",...]}
//       Create an image; rows use '1'/'#' for set and '0'/'.' for clear.
//       Response: {"ok":true,"snapshot":0}
//   {"op":"flip","snapshot":0,"x":1,"y":1,"w":2,"h":2}
//       Toggle the rectangle, creating a new snapshot.
//       Response: {"ok":true,"snapshot":1,"examined":N,"changedCount":M,
//                  "changed":[{"row":..,"col":..,"old":CELL,"new":CELL},...]}
//   {"op":"query","snapshot":0,"row":1,"col":2}
//       Response: {"ok":true,"cell":CELL}
//   {"op":"field","snapshot":0}
//       Response: {"ok":true,"width":W,"height":H,"cells":[[CELL,...],...]}
//
// CELL is {"dist2":D,"src":[row,col]} or {"dist2":null,"src":null} when the
// grid has no opposite-value pixel. Old snapshot ids stay valid forever.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sedt"
)

type request struct {
	Op       string   `json:"op"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Rows     []string `json:"rows"`
	Snapshot int      `json:"snapshot"`
	X        int      `json:"x"`
	Y        int      `json:"y"`
	W        int      `json:"w"`
	H        int      `json:"h"`
	Row      int      `json:"row"`
	Col      int      `json:"col"`
}

func errResp(err error) map[string]interface{} {
	return map[string]interface{}{"ok": false, "error": err.Error()}
}

func cellJSON(c sedt.Cell) map[string]interface{} {
	if !c.HasTarget {
		return map[string]interface{}{"dist2": nil, "src": nil}
	}
	return map[string]interface{}{
		"dist2": c.Dist2,
		"src":   [2]int{c.Src.Row, c.Src.Col},
	}
}

func parseRows(rows []string) (w, h int, cells []bool, err error) {
	h = len(rows)
	if h == 0 {
		return 0, 0, nil, fmt.Errorf("rows must be non-empty")
	}
	w = len(rows[0])
	if w == 0 {
		return 0, 0, nil, fmt.Errorf("rows must be non-empty")
	}
	for y, row := range rows {
		if len(row) != w {
			return 0, 0, nil, fmt.Errorf("row %d has width %d, want %d", y, len(row), w)
		}
		for x := 0; x < w; x++ {
			switch row[x] {
			case '1', '#':
				cells = append(cells, true)
			case '0', '.':
				cells = append(cells, false)
			default:
				return 0, 0, nil, fmt.Errorf("row %d col %d: bad char %q", y, x, row[x])
			}
		}
	}
	return w, h, cells, nil
}

func handle(store *sedt.Store, req request) map[string]interface{} {
	switch req.Op {
	case "new":
		w, h, cells, err := parseRows(req.Rows)
		if err != nil {
			return errResp(err)
		}
		sn, err := store.NewImage(w, h, cells)
		if err != nil {
			return errResp(err)
		}
		return map[string]interface{}{"ok": true, "snapshot": sn.ID}

	case "flip":
		sn, res, err := store.Flip(req.Snapshot, req.X, req.Y, req.W, req.H)
		if err != nil {
			return errResp(err)
		}
		changed := make([]map[string]interface{}, 0, len(res.Changed))
		for _, ch := range res.Changed {
			changed = append(changed, map[string]interface{}{
				"row": ch.Row,
				"col": ch.Col,
				"old": cellJSON(ch.Old),
				"new": cellJSON(ch.New),
			})
		}
		return map[string]interface{}{
			"ok":           true,
			"snapshot":     sn.ID,
			"examined":     res.Examined,
			"changedCount": len(res.Changed),
			"changed":      changed,
		}

	case "query":
		sn, err := store.Snapshot(req.Snapshot)
		if err != nil {
			return errResp(err)
		}
		if req.Row < 0 || req.Row >= sn.H || req.Col < 0 || req.Col >= sn.W {
			return errResp(fmt.Errorf("pixel (%d,%d) outside %dx%d image",
				req.Row, req.Col, sn.W, sn.H))
		}
		return map[string]interface{}{"ok": true, "cell": cellJSON(sn.At(req.Row, req.Col))}

	case "field":
		sn, err := store.Snapshot(req.Snapshot)
		if err != nil {
			return errResp(err)
		}
		rows := make([][]map[string]interface{}, sn.H)
		for y := 0; y < sn.H; y++ {
			row := make([]map[string]interface{}, sn.W)
			for x := 0; x < sn.W; x++ {
				row[x] = cellJSON(sn.At(y, x))
			}
			rows[y] = row
		}
		return map[string]interface{}{
			"ok": true, "width": sn.W, "height": sn.H, "cells": rows,
		}

	default:
		return errResp(fmt.Errorf("unknown op %q", req.Op))
	}
}

func main() {
	store := sedt.NewStore()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			enc.Encode(errResp(err))
			continue
		}
		if err := enc.Encode(handle(store, req)); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
}
