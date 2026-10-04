package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// 用法: parking-planner [input.json]
// 不带参数时从标准输入读取 JSON，结果以 JSON 输出到标准输出。
func main() {
	var data []byte
	var err error
	switch len(os.Args) {
	case 1:
		data, err = ioutil.ReadAll(os.Stdin)
	case 2:
		data, err = ioutil.ReadFile(os.Args[1])
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [input.json]\n", os.Args[0])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		os.Exit(1)
	}

	var p Problem
	if err := json.Unmarshal(data, &p); err != nil {
		fmt.Fprintf(os.Stderr, "parse input: %v\n", err)
		os.Exit(1)
	}

	res, err := Solve(&p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "solve: %v\n", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
