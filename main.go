package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	var (
		data []byte
		err  error
	)
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取输入失败:", err)
		os.Exit(1)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "解析 JSON 失败:", err)
		os.Exit(1)
	}
	if err := in.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "输入非法:", err)
		os.Exit(1)
	}

	res := solve(&in)
	if !res.Reachable {
		fmt.Println("不可达：在给定电量、营业时间与费用约束下无法到达终点")
		os.Exit(2)
	}
	fmt.Printf("最早到达时间: %d\n", res.Arrival)
	fmt.Printf("最低总费用:   %d\n", res.Cost)
	fmt.Println("行程:")
	for i, s := range res.Steps {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
}
