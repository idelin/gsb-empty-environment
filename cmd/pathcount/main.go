// Command pathcount is a JSON-in/JSON-out CLI over package pathcount.
//
// Input (stdin):
//
//	{"n":5,"edges":[[0,1],[1,2]],"s":0,"t":2,"must":[1],
//	 "budget":-1,"op":"count"}
//	{"...same version...","op":"page","prefix":[0],"offset":"0",
//	 "limit":10,"token":"<from count output>"}
//
// Output (stdout) for count: {"token":"...","count":"3"} or
// {"token":"...","unknown":true}. For page: {"token":"...",
// "total":"3","offset":"0","paths":[[0,1,2]]} possibly with
// "unknown":true when the budget ran out.
package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"pathcount/pathcount"
)

type request struct {
	N      int      `json:"n"`
	Edges  [][2]int `json:"edges"`
	S      int      `json:"s"`
	T      int      `json:"t"`
	Must   []int    `json:"must"`
	Budget int64    `json:"budget"`
	Op     string   `json:"op"` // "count" or "page"
	Prefix []int    `json:"prefix"`
	Offset string   `json:"offset"`
	Limit  int      `json:"limit"`
	Token  string   `json:"token"`
}

type response struct {
	Token   string  `json:"token"`
	Count   string  `json:"count,omitempty"`
	Total   string  `json:"total,omitempty"`
	Offset  string  `json:"offset,omitempty"`
	Paths   [][]int `json:"paths,omitempty"`
	Unknown bool    `json:"unknown,omitempty"`
	Err     string  `json:"error,omitempty"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fail("bad request: " + err.Error())
	}
	v, err := pathcount.NewVersion(req.N, req.Edges, req.S, req.T, req.Must)
	if err != nil {
		fail(err.Error())
	}
	resp := response{Token: v.Token()}
	c := pathcount.NewCounter(v, req.Budget)
	switch req.Op {
	case "count":
		n, err := c.Count()
		if err != nil {
			resp.Unknown = true
		} else {
			resp.Count = n.String()
		}
	case "page":
		offset := new(big.Int)
		if req.Offset != "" {
			if _, ok := offset.SetString(req.Offset, 10); !ok {
				fail("bad offset")
			}
		}
		token := req.Token
		if token == "" {
			token = v.Token()
		}
		page, err := c.Paginate(req.Prefix, offset, req.Limit, token)
		if err != nil && page == nil {
			fail(err.Error())
		}
		resp.Unknown = page.Unknown
		if page.Total != nil {
			resp.Total = page.Total.String()
		}
		resp.Offset = page.Offset.String()
		resp.Paths = page.Paths
	default:
		fail("op must be \"count\" or \"page\"")
	}
	out, _ := json.Marshal(resp)
	fmt.Println(string(out))
}

func fail(msg string) {
	out, _ := json.Marshal(response{Err: msg})
	fmt.Println(string(out))
	os.Exit(1)
}
