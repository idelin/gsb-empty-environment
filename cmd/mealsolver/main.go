// mealsolver 从 JSON 文件（或标准输入）读取配置与菜品，输出最优五日餐单；
// 无解时输出冲突约束子集。
//
// 用法:
//   mealsolver [input.json]   省略参数时从 stdin 读取
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"mealsolver"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}
	var in struct {
		Config mealsolver.Config `json:"config"`
		Dishes []mealsolver.Dish `json:"dishes"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析 JSON 失败: %v", err)
	}
	out, err := json.MarshalIndent(mealsolver.Solve(in.Config, in.Dishes), "", "  ")
	if err != nil {
		fatal("序列化结果失败: %v", err)
	}
	fmt.Println(string(out))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
