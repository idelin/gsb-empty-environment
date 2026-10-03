# decor — 关联子查询解除（Subquery Unnesting / Decorrelation）

用 Go 标准库实现的子查询去相关化重写器：把低效的相关子查询展开为常规
Join，消除"每行重新执行一遍子查询"的性能陷阱。

## 功能

- 手写 SQL 词法 / 语法分析器（`SELECT ... FROM ... WHERE ...`，支持
  `EXISTS`、`IN`、`NOT IN`、比较与 `AND/OR/NOT` 谓词）
- 去相关化重写规则：
  - 相关 `EXISTS` → **Semi-Join**（等值相关谓词变为 Join key）
  - 相关 / 非相关 `IN (SELECT ...)` → **Semi-Join**（`x = 子查询投影列` 为主 key）
  - 非相关 `EXISTS` → `CROSS JOIN (子查询 LIMIT 1)`
  - 非等值相关谓词（如 `e.salary > d.min_salary`）→ 明确报错，不做错误重写
  - `NOT IN`（NULL 语义）→ 保守保留，不重写
- 生成逻辑执行计划：`HashSemiJoin / Distinct / Project / Filter / Scan`
- 内置迷你内存执行引擎：同一查询可分别走"朴素逐行相关求值"与
  "去相关化 Hash Semi-Join"两条路径，验证结果一致并对比代价

## 验收样例

输入：

```sql
SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)
```

重写后（相关性已消除）：

```sql
SELECT d.* FROM depts d SEMI JOIN (SELECT DISTINCT e.dept_id FROM emps e) e ON d.id = e.dept_id
```

逻辑计划：

```
HashSemiJoin ON d.id = e.dept_id
├─ Scan depts AS d
└─ Distinct
   └─ Project e.dept_id
      └─ Scan emps AS e
```

## 运行

```sh
go test ./...      # 运行全部测试
go run ./cmd/demo  # 运行演示（重写 + 计划 + 双路径执行对比）
```

## 演示输出（实测）

```
=== Subquery Unnesting / Decorrelation ===

Input query:
  SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)

Rules applied:
  - EXISTS decorrelated -> SEMI JOIN on d.id = e.dept_id

Rewritten query:
  SELECT d.* FROM depts d SEMI JOIN (SELECT DISTINCT e.dept_id FROM emps e) e ON d.id = e.dept_id

Logical plan:
HashSemiJoin ON d.id = e.dept_id
├─ Scan depts AS d
└─ Distinct
   └─ Project e.dept_id
      └─ Scan emps AS e

IN example:
  SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')
  ->
  SELECT e.* FROM emps e SEMI JOIN (SELECT DISTINCT d.id FROM depts d WHERE d.region = 'EU') d ON e.dept_id = d.id

Data: 200 departments, 4000 employees (half the departments are empty)

Execution comparison (results verified identical):
  naive correlated loop : 7.7s    (100 rows, 405050 inner predicate evaluations)
  decorrelated hash join: 447ms   (100 rows, 4000 build + 200 probe operations)
  speedup               : 17x
```

朴素路径对每个外层行都重扫子查询（O(N×M) 次谓词求值），去相关化后
只需一次 Hash build + 一次 probe（O(N+M)），结果集经程序校验完全一致。

## 代码结构

| 文件 | 职责 |
| --- | --- |
| `lexer.go` / `parser.go` | SQL 词法与递归下降语法分析 |
| `ast.go` | 表达式与 SELECT 语句 AST |
| `render.go` | AST → SQL 文本渲染 |
| `decorrelate.go` | 核心：相关谓词切分、Semi-Join 重写规则 |
| `plan.go` | 逻辑计划节点与树形打印 |
| `exec.go` | 内存执行器（朴素相关求值 + 计划执行） |
| `cmd/demo/main.go` | 端到端演示 |
