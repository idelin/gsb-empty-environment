# earley

通用上下文无关文法（CFG）Earley 解析器，仅使用 Go 标准库。

- 支持任意 CFG：左递归、空产生式（Epsilon）、二义性文法
- 输出完整解析图（Chart）
- 二义性时枚举所有合法解析树（packed forest + 回溯枚举）
- 提供按算符优先级 / 左结合性的消歧过滤器

## 运行样例

```sh
go run ./cmd/demo
```

解析 `a + b * c`（文法 `E -> E + E | E * E | id`），打印完整 Chart、
2 棵语法树，以及消歧结果 `a + (b * c)`。

## 测试

```sh
go test ./...
```

## API 速览

```go
g := earley.NewGrammar("E",
    earley.R("E", "E", "+", "E"),
    earley.R("E", "E", "*", "E"),
    earley.R("E", "id"),
)
chart := g.Parse(tokens)          // tokens: []earley.Token{{Type: "id", Value: "a"}, ...}
chart.Accepted()                  // 是否接受
fmt.Print(chart.String())         // 完整 Chart
trees := chart.ParseTrees()       // 枚举所有解析树
kept := earley.Disambiguate(trees, map[string]int{"+": 1, "*": 2})
```
