// Package cli exposes a line-delimited JSON command interface over the
// edtgrid engine. One JSON request per line on stdin, one JSON response
// per line on stdout.
//
// Commands:
//   {"cmd":"new","rows":5,"cols":5,"fill":0}
//   {"cmd":"set","row":2,"col":2,"value":1}
//   {"cmd":"flip","row0":1,"col0":1,"row1":3,"col1":3}   // half-open rect
//   {"cmd":"query","row":0,"col":0,"version":0}          // version optional
//   {"cmd":"dump","version":1}                           // version optional
//
// Every edit creates a new immutable version; queries against older
// versions return the old distances.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"edtgrid"
)

type request struct {
	Cmd     string `json:"cmd"`
	Rows    int    `json:"rows"`
	Cols    int    `json:"cols"`
	Fill    uint8  `json:"fill"`
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	Value   uint8  `json:"value"`
	R0      int    `json:"row0"`
	C0      int    `json:"col0"`
	R1      int    `json:"row1"`
	C1      int    `json:"col1"`
	Version *int   `json:"version"`
}

type response map[string]interface{}

// Run processes commands from in until EOF and writes responses to out.
func Run(in io.Reader, out io.Writer) error {
	s := &session{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := enc.Encode(errResponse("invalid JSON: " + err.Error())); err != nil {
				return err
			}
			continue
		}
		if err := enc.Encode(s.dispatch(req)); err != nil {
			return err
		}
	}
	return sc.Err()
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func errResponse(msg string) response {
	return response{"ok": false, "error": msg}
}

type session struct {
	versions []*edtgrid.Grid
}

func (s *session) dispatch(req request) response {
	switch req.Cmd {
	case "new":
		return s.cmdNew(req)
	case "set":
		return s.cmdSet(req)
	case "flip":
		return s.cmdFlip(req)
	case "query":
		return s.cmdQuery(req)
	case "dump":
		return s.cmdDump(req)
	default:
		return errResponse("unknown cmd %q (want new|set|flip|query|dump)" + "")
	}
}

func (s *session) latest() (*edtgrid.Grid, int, response) {
	if len(s.versions) == 0 {
		return nil, 0, errResponse("no grid yet: send {\"cmd\":\"new\",...} first")
	}
	return s.versions[len(s.versions)-1], len(s.versions) - 1, nil
}

func (s *session) pick(v *int) (*edtgrid.Grid, int, response) {
	if v == nil {
		return s.latest()
	}
	if *v < 0 || *v >= len(s.versions) {
		return nil, 0, errResponse(fmt.Sprintf("version %d out of range [0,%d)", *v, len(s.versions)))
	}
	return s.versions[*v], *v, nil
}

func (s *session) cmdNew(req request) response {
	g, err := edtgrid.New(req.Rows, req.Cols, req.Fill)
	if err != nil {
		return errResponse(err.Error())
	}
	s.versions = []*edtgrid.Grid{g}
	return response{"ok": true, "version": 0, "rows": req.Rows, "cols": req.Cols}
}

func (s *session) pushEdit(g *edtgrid.Grid, d edtgrid.Diff) response {
	version := len(s.versions) - 1
	if !d.NoOp {
		s.versions = append(s.versions, g)
		version = len(s.versions) - 1
	}
	return response{
		"ok":             true,
		"version":        version,
		"noOp":           d.NoOp,
		"fullRecompute":  d.FullRecompute,
		"examinedPixels": d.ExaminedPixels,
		"changedCount":   len(d.Changed),
		"changed":        d.Changed,
	}
}

func (s *session) cmdSet(req request) response {
	g, _, errResp := s.latest()
	if errResp != nil {
		return errResp
	}
	if req.Value > 1 {
		return errResponse("value must be 0 or 1")
	}
	if !g.InBounds(req.Row, req.Col) {
		return errResponse(fmt.Sprintf("pixel (%d,%d) out of bounds", req.Row, req.Col))
	}
	if g.Value(req.Row, req.Col) == req.Value {
		return s.pushEdit(g, edtgrid.Diff{NoOp: true})
	}
	ng, d := g.Flip(edtgrid.Rect{R0: req.Row, C0: req.Col, R1: req.Row + 1, C1: req.Col + 1})
	return s.pushEdit(ng, d)
}

func (s *session) cmdFlip(req request) response {
	g, _, errResp := s.latest()
	if errResp != nil {
		return errResp
	}
	ng, d := g.Flip(edtgrid.Rect{R0: req.R0, C0: req.C0, R1: req.R1, C1: req.C1})
	return s.pushEdit(ng, d)
}

func cellJSON(g *edtgrid.Grid, row, col int) response {
	c := g.At(row, col)
	r := response{
		"row":       row,
		"col":       col,
		"value":     c.Value,
		"hasTarget": c.HasTarget,
	}
	if c.HasTarget {
		r["dist"] = c.Dist
		r["source"] = c.Source
	} else {
		r["dist"] = edtgrid.NoTargetDist
	}
	return r
}

func (s *session) cmdQuery(req request) response {
	g, v, errResp := s.pick(req.Version)
	if errResp != nil {
		return errResp
	}
	if !g.InBounds(req.Row, req.Col) {
		return errResponse(fmt.Sprintf("pixel (%d,%d) out of bounds", req.Row, req.Col))
	}
	r := cellJSON(g, req.Row, req.Col)
	r["ok"] = true
	r["version"] = v
	return r
}

func (s *session) cmdDump(req request) response {
	g, v, errResp := s.pick(req.Version)
	if errResp != nil {
		return errResp
	}
	cells := make([]response, 0, g.Rows()*g.Cols())
	for row := 0; row < g.Rows(); row++ {
		for col := 0; col < g.Cols(); col++ {
			cells = append(cells, cellJSON(g, row, col))
		}
	}
	return response{
		"ok":      true,
		"version": v,
		"rows":    g.Rows(),
		"cols":    g.Cols(),
		"cells":   cells,
	}
}
