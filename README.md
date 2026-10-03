# earley — 通用上下文无关文法 Earley 解析器（Go，仅标准库）

完整的 Earley 分析算法实现，支持任意 CFG：

- 左递归 / 右递归文法
- 空产生式（epsilon）与可空非终结符
- 二义性文法：输出完整解析图（Chart），并枚举**所有**合法解析树
- 基于算符优先级 / 结合性的消歧过滤

## 文件结构

- `grammar.go` — 文法与产生式定义
- `parser.go` — Earley 核心算法（预测 / 扫描 / 完成），生成完整 Chart
- `forest.go` — 沿回指针枚举全部解析树
- `disambig.go` — 优先级 + 结合性消歧
- `main.go` — 运行样例：`E -> E + E | E * E | id`，输入 `a + b * c`
- `parser_test.go` — `go test` 测试

## 运行

```sh
go run .        # 打印 Chart、2 棵解析树、消歧结果
go test -v ./... # 运行全部测试
```

## 验收样例输出（节选）

输入 `a + b * c`，二义文法产生 2 棵合法语法树：

```
(E (E (E id[a]) + (E id[b])) * (E id[c]))   -- (a+b)*c
(E (E id[a]) + (E (E id[b]) * (E id[c])))   -- a+(b*c)
```

按 `* > +`、左结合消歧后保留 1 棵：`a + (b * c)`。

测试结果：9 个用例全部 PASS（二义性枚举、epsilon、空输入、
左/右递归、拒绝非法输入、Chart 完整性、左结合消歧等）。
