# condbuilder — 数据访问层条件构造组件

`condbuilder` 是一个只生成只读查询的条件构造器，核心原则是 **标识符走白名单、值走占位符、成品做结构校验**。

## 安全模型

- **字段白名单**：所有列名/表名必须先在 `Whitelist` 中声明，并匹配 `^[A-Za-z_][A-Za-z0-9_]*$`；外部输入永远不能进入标识符位置，渲染时只输出反引号引用的安全标识符。
- **参数绑定**：筛选值一律以 `?` 占位符返回，与 SQL 文本分离，从不做字符串拼接。
- **模糊匹配转义**：`Contains`/`StartsWith`/`EndsWith` 对 `%`、`_`、`\` 做转义，并强制输出 `ESCAPE '\'` 子句，用户输入按字面量匹配。
- **结构校验**：`ValidateStructure` 对拼接结果做词法扫描——单语句、括号配平、禁止 `;`、禁止 DDL/DML 关键字（`DROP`/`DELETE`/`INSERT` 等）、引号标识符二次校验。
- **批量查询**：`IN`/`NOT IN` 接收非空 `[]any` 并逐元素展开为占位符；空批次与 `nil` 元素直接报错，避免 `IN ()` 或隐式 `IS NULL` 语义。
- **联合查询**：`UnionBuilder` 要求每个分支持有 **同一个白名单实例**、投影列序列完全一致；禁止分支级 `ORDER BY`；整体排序字段必须属于结果集列，表中存在但未投影的列（如 `secret`）会被拒绝。

## 快速示例

```go
wl, _ := condbuilder.NewWhitelist("id", "users.name", "email", "status", "created_at")

sel, _ := condbuilder.NewSelect(wl, "users", []string{"id", "users.name", "email"})
sql, args, err := sel.
    Where(condbuilder.Contains("email", "100%")).        // `email` LIKE ? ESCAPE '\'
    Where(condbuilder.In("status", []any{"a", "b"})).   // `status` IN (?, ?)
    OrderBy(condbuilder.ByDesc("created_at")).
    Limit(10).
    Build()
// sql 与 args 分离，可直接交给 database/sql 的 QueryContext

left, _ := condbuilder.NewSelect(wl, "users", []string{"id", "email"})
right, _ := condbuilder.NewSelect(wl, "admins", []string{"id", "email"})
union, _ := condbuilder.NewUnion(left, condbuilder.UnionAll)
union.Add(right)
union.OrderBy(condbuilder.By("email")) // 仅允许结果集内的列
unionSQL, unionArgs, err := union.Build()
```

## 验证

`go test ./...` 覆盖白名单注入、`LIKE` 转义、排序注入、空/`nil` 批次、结构校验以及联合查询绕过（异白名单、列不匹配、结果集外排序、分支级排序）等场景。
