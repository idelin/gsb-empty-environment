# Subquery Unnesting / Decorrelation (Go)

将低效的相关子查询展开为常规 Join，消除逐行重复执行子查询的性能陷阱。
仅使用 Go 标准库，无任何第三方依赖。

## 功能

- 手写 SQL 词法分析器 + 递归下降语法分析器（`SELECT ... FROM ... WHERE ...`，
  支持 `EXISTS` / `IN` 子查询、比较运算、`AND`/`OR`、表别名、字符串/数字字面量）。
- 解关联重写器 `Unnest`：
  - 相关 `EXISTS` → **Hash Semi-Join**（相关等值谓词成为 join key）
  - 相关 `IN` → **Hash Semi-Join**（IN 列 + 相关谓词共同作为 join key）
  - 非相关 `IN` → **Hash Inner-Join**，右侧加 `Distinct` 去重以保持语义
  - 子查询中只涉及内层表的谓词下推为内层 `Filter`
  - 非等值相关谓词保留为 join 的 residual 条件
  - 外层其余谓词保留为顶层 `Filter`
- 计划展示：`Explain` 输出算子树，`RenderSQL` 输出伪 SQL（`SEMI JOIN` 为算子记号，非标准 SQL）。

## 运行样例

```sh
go run ./cmd/demo
```

输出（节选，验收用例）：

```
=== Query 1 ===
input:     SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)
rewritten: SELECT * FROM depts d SEMI JOIN emps e ON d.id = e.dept_id
plan:
Project(*)
└─ HashSemiJoin(d.id = e.dept_id)
   ├─ Scan(depts d)
   └─ Scan(emps e)
```

## 测试

```sh
go test ./...
```

结果：

```
ok  	decorrelator	0.664s
```

共 11 个测试（含 5 个错误场景子测试），覆盖：EXISTS 解关联为 Hash Semi-Join
（验收用例）、内层谓词下推、外层残余谓词、相关/非相关 IN、多 EXISTS 链式、
非等值相关谓词 residual、无限定名列归属内层、非法输入报错、解析器基础行为。

## 代码结构

- `lexer.go` — 词法分析
- `parser.go` — 递归下降语法分析，生成 AST
- `ast.go` — AST 节点定义与表达式打印
- `unnest.go` — 解关联重写核心（谓词分类：相关等值 / 相关其他 / 纯内层 / 纯外层）
- `plan.go` — 逻辑计划算子、`Explain` 树形展示、`RenderSQL` 伪 SQL 渲染
- `unnest_test.go` — 单元测试
- `cmd/demo/main.go` — 可运行示例
