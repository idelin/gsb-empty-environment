# LL(k) 前瞻预测分析器

基于固定 K 步前瞻的表驱动递归下降分析器（Go 标准库，无第三方依赖）。

## 设计

- `llk/grammar.go`：文法描述（终结符 `T`、非终结符 `N`、空串 `Eps`、产生式、文法）。
- `llk/firstk.go`：FIRST_k 与 FOLLOW_k 的不动点计算（K 截断连接 ⊗_k）。
- `llk/table.go`：预测表构建。候选 `A->α` 的表项键为 `FIRST_k(α) ⊗_k FOLLOW_k(A)`。
  若同一非终结符的两条候选共享 K 步前瞻（文法不是 LL(k)），返回 `*LLKError`（文法超界异常），不构建表。
- `llk/parser.go`：表驱动递归下降解析。每个非终结符查表展开一次、每个 Token 消费一次，
  无回溯，O(n) 线性；输入不合法时返回 `*ParseError`。

## 验收边界

文法 `A -> 'a' 'b' 'c' | 'a' 'b' 'd'`：

- K=2：两候选 FIRST_2 均为 `{a b}`，构建时抛出 `LLKError`。
- K=3：FIRST_3 分别为 `{a b c}`、`{a b d}`，预测表构建成功，线性解析 `a b c` / `a b d`。

## 运行

```sh
go test ./llk/ -v   # 测试
go run ./cmd/demo   # 演示
```
