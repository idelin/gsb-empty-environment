# relalg — 关系代数逻辑算子树（Go）

仅使用标准库实现的 SQL 查询核心 AST → 逻辑执行计划树组件，包含：

- **SQL 解析**：`SELECT ... FROM t [AS] a [[INNER|LEFT|RIGHT] JOIN ... ON ...]* [WHERE ...]`，
  表达式优先级 `OR < AND < 比较运算`（`lexer.go`、`parser.go`、`ast.go`、`expr.go`）。
- **逻辑算子树**：`Scan` / `Project` / `Filter` / `Join`，
  支持先序遍历 `Walk` 与树形序列化打印 `Format`（`plan.go`）。
- **计划转换器**：`ParseSelect` → `BuildPlan` 生成嵌套逻辑算子树（`planner.go`）。
- **规则优化器** `Optimize`（`optimizer.go`）：
  - 谓词关联下推：Filter 合取项按引用列归属下推到 Join 两侧，
    跨两侧等值条件合并进 Join 的 ON；
  - 投影消除：合并嵌套 Project，移除输出与子算子完全一致的恒等投影。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
SQL:
  SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18

逻辑计划（优化前）:
Project(u.name)
└── Filter(u.age > 18)
    └── Join(INNER, u.id = o.uid)
        ├── Scan(users AS u)
        └── Scan(orders AS o)

逻辑计划（优化后：谓词下推 + 投影消除）:
Project(u.name)
└── Join(INNER, u.id = o.uid)
    ├── Filter(u.age > 18)
    │   └── Scan(users AS u)
    └── Scan(orders AS o)
```

也可传入自定义 SQL：`go run ./cmd/demo "SELECT * FROM a JOIN b ON a.x = b.x WHERE a.z > 5"`。

## 测试

```sh
go test ./...
```

结果：13 个测试全部通过（解析、优先级、树形结构、序列化打印、遍历顺序、
Schema 推导、谓词下推、ON 条件合并、残余谓词保留、投影消除、优化幂等性）。

```
PASS
ok  	relalg	0.857s
```

## 说明

- 无系统目录（catalog），`Scan.Columns` 取查询中实际引用的列集合（按列名排序），
  用于 Schema 推导与恒等投影判定。
- 含未限定列（无表前缀）的谓词保守保留在 Join 之上，不下推。
