# Tableland 写入策略强制包

给 Go 执行层使用的表级写入策略强制库。写入语句以**已解析的语法树**传入，
所有改写都在 AST 上完成，绝不拼接 SQL 字符串。

## 包结构

- `ast` — 写入语句的语法树类型（`UpdateStmt` / `DeleteStmt` / `InsertStmt`
  / `MultiStmt` 等）、表达式节点、构造器与规范化 SQL 渲染。
- `policy` — 策略定义、强制入口 `Enforcer.Enforce`、表达式求值器
  （SQL 三值逻辑）、事务接口 `Store`/`Txn` 与可回滚的内存实现
  `MemoryCatalog`。

## 强制规则

1. **WHERE 结构性 AND 合并**：`MergeWhere` 把策略谓词与用户谓词合成新的
   AST 子树，用户谓词整体包一层 `ParenExpr` 再 `AND` 策略谓词。用户写入的
   `OR 1=1` 只能在括号内生效，无法放宽策略圈定的行集。
2. **可更新列白名单**：`UPDATE SET` 的每个目标列必须在
   `Policy.UpdatableColumns` 内，否则返回 `ErrForbiddenColumn`，语句不执行。
3. **WITH CHECK 逐行求值**：`INSERT` 的每个新行、`UPDATE` 赋值后的行都要令
   `Policy.WithCheck` 为真；任一行不满足即返回 `ErrCheckViolation` 并回滚
   整条语句（NULL 与 false 一样视为不满足）。
4. **影响行数上限**：`Policy.MaxRows`（`UnlimitedRowCount` 表示不限）在任何
   变更发布前校验，超限返回 `ErrRowLimit` 并回滚。
5. **结构性拒绝**：多语句（`MultiStmt` 或 `EnforceBatch` 多条）→
   `ErrMultiStatement`；子查询 → `ErrSubquery`；引用其他表或带其他表限定符
   的列 → `ErrForeignTable`；裸 `SELECT` → `ErrUnsupportedStatement`。
6. **策略表达式白名单**：策略的 `Where` / `WithCheck` 在构造 `Enforcer` 时
   校验，只允许列、字面量、`caller_addr()`、`block_num()` 及普通运算符，
   函数调用、子查询等动态节点返回 `ErrPolicyExpression`。

## 用法

```go
enf, err := policy.NewEnforcer(policy.Policy{
    Table:            "assets",
    UpdatableColumns: []string{"status"},
    Where:            ast.Eq(ast.Col("owner"), ast.Caller()),
    WithCheck:        ast.Eq(ast.Col("owner"), ast.Caller()),
    MaxRows:          100,
}, catalog)

// stmt 是解析后的 *ast.UpdateStmt / *ast.DeleteStmt / *ast.InsertStmt。
affected, err := enf.Enforce(policy.Context{Caller: "0xabc", BlockNum: 42}, stmt)
```

执行层实现 `policy.Store`/`policy.Txn` 即可接入真实存储；任何策略失败都会
调用 `Txn.Rollback`，保证不留半成品写入。

## 验证

`go test ./...` 覆盖五类攻击/越权场景（均断言失败后存储无变化）：

- `OR 1=1` 注入的 DELETE / UPDATE 只能命中调用者自己的行；
- SET 越权列在触碰数据前被拒；
- 多行 INSERT 中一行不满足 WITH CHECK，整批回滚；
- 影响行数超过 `MaxRows` 时 INSERT / DELETE 回滚；
- 多语句提交（AST 批次节点与批次入口两种形式）被拒。

另含子查询、跨表引用、策略表达式白名单、三值逻辑求值与事务回滚的测试。
