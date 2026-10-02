// Command demo parses a mini-language program, runs the attribute-grammar
// evaluator on its AST and prints type diagnostics and the final symbol
// table synthesized from the top-level statements.
package main

import (
	"fmt"
	"os"

	"attrgram/mini"
)

const src = `// 变量声明与使用：类型推导示例
var x int = 1 + 2
var y = x * 3            // 综合属性 type 自底向上推导为 int
var f float = y          // int 可赋值给 float
var s string = "hello" + " world"
var ok bool = x < y && !false
print(s)
var bad = x + s          // 类型不匹配：int + string
var u = missing + 1      // 未定义变量
var q string = 42        // 声明类型与初始化类型不匹配
`

func main() {
	fmt.Println("=== source ===")
	fmt.Print(src)

	prog, err := mini.Parse(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}

	res, err := mini.Check(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluation error:", err)
		os.Exit(1)
	}

	fmt.Println("=== diagnostics (with AST node positions) ===")
	if len(res.Diagnostics) == 0 {
		fmt.Println("(none)")
	}
	for _, d := range res.Diagnostics {
		fmt.Println(" ", d)
	}

	fmt.Println("=== final symbol table (synthesized envOut) ===")
	fmt.Println(" ", res.Env)
}
