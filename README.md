# configdiag — 有限域配置诊断器

对最多 20 条有限域约束的实例，找出一个**包含极小不可满足子集**（MUS,
minimal unsatisfiable subset），并提供一个**独立的满足性检查器**，用穷举
枚举证明诊断结果。

“极小”指**包含极小**（删去其中任一条约束，子集即可满足），而不是条数最
少；结果是众多 MUS 中的一个，由输入顺序唯一确定。

## 构建

需要 Go（兼容 Go 1.11+，仅用标准库）：

```sh
GO111MODULE=on go build ./cmd/diagnose ./cmd/verify
```

## 输入格式

实例是一个 JSON 文件：变量各有有限值域；每条约束是**表约束**，列出其变
量（按 `vars` 顺序）允许取的值元组。

```json
{
  "variables": [
    {"name": "mode", "domain": ["dev", "prod"]}
  ],
  "constraints": [
    {"name": "mode-prod", "vars": ["mode"], "allowed": [["prod"]]}
  ]
}
```

约束名必须唯一；`vars` 引用的变量必须已声明；每个允许元组的元数必须等
于 `len(vars)`，且每个值都在对应变量的值域内。`vars` 为空的约束是常
量：`"allowed": [[]]` 恒真，`"allowed": []` 恒假。示例见
`examples/config.json`。

## 诊断器：`diagnose`

```sh
./diagnose -in examples/config.json -out mus.json
```

算法是**按输入顺序的删除式缩减**：先确认整集不可满足；然后按输入顺序逐
条考察约束——若去掉它之后剩余集合仍不可满足，就永久删除，否则保留。每
一步只依赖当前集合与输入顺序，因此同一输入的缩减过程和结果完全确定。
最终剩下的集合即为一个 MUS。

- 人类可读的缩减轨迹（keep/drop 及原因）打印到 stderr；
- JSON 报告（`status`、`mus`、`reduction`）写入 `-out`（默认 stdout），
  可直接作为 `verify` 的输入；
- 若整集可满足，则报告 `"status": "satisfiable"`，不存在不可满足子集。

## 独立检查器：`verify`

```sh
./verify -in examples/config.json -mus mus.json
```

`verify` 不与诊断器共享任何搜索/求值代码：它从 JSON 重新编译约束，用递
归全枚举（无提前退出）独立验证：

1. **整集无解**：枚举 MUS 涉及变量的整个值域笛卡尔积，统计满足全部约
   束的赋值数为 0；
2. **各删一子集有解**：对 MUS 中每一条约束，枚举去掉它之后的搜索空间，
   给出一个具体满足赋值作为见证。

全部通过时退出码为 0 并打印 `RESULT: OK`；任一条件不成立（集合可满足、
或某条约束冗余）退出码为 1；用法/IO 错误退出码为 2。

也可以直接检查任意子集的可满足性：

```sh
./verify -in examples/config.json -subset db-mysql,db-pg
```

## 限制

- 诊断器最多接受 20 条约束（`diag.MaxConstraints`）。
- 穷举证明的成本是作用域变量值域的乘积。`verify` 默认在单次枚举超过
  1 亿个赋值时中止并报错（拒绝给出伪证明），可用 `-max-assignments`
  调整。

## 测试

```sh
GO111MODULE=on go test ./...
```
