# constfold — 常量折叠与表达式代数化简引擎 (Go)

仅使用 Go 标准库实现的 SQL 风格表达式预求值优化器：在"编译期"识别纯常量子树并折叠为标量值，
应用代数恒等式化简，并消除无意义的布尔条件。

## 功能

- **常量折叠**：`1 + 2 * 3` → `7`，`age > 10 + 5` → `age > 15`，`'foo' = 'foo'` → `TRUE`
- **代数恒等式**：`x + 0`→`x`、`x * 1`→`x`、`x * 0`→`0`、`x / 1`→`x`、`id = id`→`TRUE` 等
- **布尔条件消除**：`X AND TRUE`→`X`、`X AND FALSE`→`FALSE`、`X OR FALSE`→`X`、`NOT NOT x`→`x`
- **SQL 三值逻辑**：`NULL AND FALSE`→`FALSE`、`NULL OR TRUE`→`TRUE`、`NULL = 1`→`NULL`
- **安全边界**：除以零保留到运行期（`10 / (2 - 2)` → `10 / 0`）；`NOW()` 等不透明函数保留但参数仍折叠
- 不可变：`Fold` 返回新树，不改写输入 AST

## 验收样例

```
输入: u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0
输出: (u.created_at > (NOW() - INTERVAL 1 DAY))
```

`u.score * 0` 经代数规则化为 `0`，`0 = 0` 折叠为 `TRUE`，`1 = 1` 折叠为 `TRUE`，
两个 `AND TRUE` 被消除，最终化简为单变量不等式过滤。

## 结构

- `ast.go` — AST 节点（Literal / Column / Binary / Unary / FuncCall / Interval）
- `parser.go` — 词法分析 + 递归下降解析器（OR/AND/NOT/比较/算术/INTERVAL/函数调用）
- `fold.go` — 折叠引擎入口 `Fold` / `FoldString`
- `fold_test.go` — go test 测试
- `cmd/demo/main.go` — 可运行样例

## 运行

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行样例
```
