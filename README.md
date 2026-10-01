# logplan — 关系代数逻辑算子树（Go）

仅使用 Go 标准库实现：SQL 核心子集解析 → AST → 逻辑执行计划树
（Scan / Project / Filter / Join），支持谓词下推、投影消除、
前序遍历（`Walk`）与树形序列化打印（`String`）。

## 结构

- `ast.go` — SQL 核心 AST（SelectStmt / TableRef / JoinClause / Expr）
- `parser.go` — 词法 + 递归下降解析器（SELECT ... FROM ... JOIN ... ON ... WHERE ...）
- `plan.go` — 逻辑算子节点、`Walk` 遍历、树形打印
- `builder.go` — AST → 逻辑计划转换器（`Build` / `BuildOptimized` / `Optimize`）
- `optimize.go` — 谓词关联下推（按 AND 拆分合取项，单表条件下推到对应 Scan，
  跨表条件并入 Join 条件）与投影消除（`SELECT *`、`t.*`、嵌套 Project 折叠）
- `cmd/demo` — 运行样例
- `logplan_test.go` — `go test` 测试

## 运行样例

```console
$ go run ./cmd/demo
SQL:
  SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18

Logical plan (unoptimized):
Project(u.name)
└── Filter(u.age > 18)
    └── Join(u.id = o.uid)
        ├── Scan(users AS u)
        └── Scan(orders AS o)
Logical plan (optimized: predicate pushdown + projection elimination):
Project(u.name)
└── Join(u.id = o.uid)
    ├── Filter(u.age > 18)
    │   └── Scan(users AS u)
    └── Scan(orders AS o)
```

## 测试

```console
$ go test ./...
ok  	logplan
?   	logplan/cmd/demo	[no test files]
```
