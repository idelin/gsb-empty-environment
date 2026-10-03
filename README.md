# fold — 常量折叠与表达式代数化简引擎 (Go)

仅用 Go 标准库实现的 SQL 表达式预求值优化器：解析 WHERE 风格表达式为 AST，
自底向上重写为等价的最小形式。

## 能力

- **常量折叠**：纯常量子树在编译期求值为标量
  - `1 + 2 * 3` → `7`，`age > 10 + 5` → `age > 15`
- **代数恒等式**：`x * 1 → x`、`x + 0 → x`、`x * 0 → 0`、`NOT NOT x → x`
- **布尔消元**：`x AND TRUE → x`、`x OR FALSE → x`、`1 = 1 → TRUE`（再被 AND 吸收）
- **非确定性函数保留**：`NOW()` 等视为不透明，不折叠；其参数中的常量仍会折叠
  （`INTERVAL (1+1) DAY` → `INTERVAL 2 DAY`）
- 除零不折叠，保持原样留待运行期报错

## 验收边界

```
input : WHERE u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0
output: WHERE u.created_at > NOW() - INTERVAL 1 DAY
```

推导链：`u.score * 0 → 0`，`0 = 0 → TRUE`，`1 = 1 → TRUE`，两个 `TRUE` 被 `AND` 吸收，
`NOW() - INTERVAL 1 DAY` 因含非确定性函数而保留。

## 结构

- `fold/expr.go` — AST 节点定义与 SQL 渲染（`String`）
- `fold/parser.go` — 词法分析 + 递归下降解析（`Parse`）
- `fold/simplify.go` — 化简引擎（`Simplify`）
- `fold/simplify_test.go` — `go test` 测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例
```

## 说明

化简规则采用二值逻辑（不考虑 NULL 三值语义），与练习范围一致；
接入真实 SQL 优化器时需为 `x * 0 → 0` 等规则补充 NULL 传播约束。
