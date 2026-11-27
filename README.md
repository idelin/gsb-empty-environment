企业身份中台通过 SCIM 2.0（RFC 7643/7644）同步用户与组时，若 `filter` 查询解析器直接拼接条件或 `PATCH` 操作未限制只读/特权属性（如 `roles`/`groups`/`active`/`externalId`）以及跨租户 `members` 引用，低权限租户管理员可通过构造嵌套过滤条件枚举全局用户或借 `PATCH` 将自身加入系统管理员组。为 Go 身份同步宿主实现可导入的 RFC 7644 SCIM 过滤器递归下降解析器与租户隔离策略引擎，严禁依赖外部解析库。组件完整解析 SCIM `filter` 表达式（`eq`/`ne`/`co`/`sw`/`ew`/`pr`/`gt`/`ge`/`lt`/`le`、`and`/`or`/`not` 及 `attr[subFilter]` 值路径），强制限定最大 AST 深度、禁止查询未授权敏感属性并绑定隐式租户隔离谓词；对 `PATCH`（`add`/`remove`/`replace`）路径表达式校验属性可变性（`readOnly`/`immutable` 拒绝修改）与特权组归属边界，确保任何跨租户成员挂载或只读属性篡改均被原子回滚并拒收。附过滤器注入、跨租户组提权与不可变属性修改测试。

## 实现概览

模块根目录即 `package scim`，仅依赖 Go 标准库：

```go
import scim "formal.local/w27-4e139ba19e"
// 也提供完全等价的再导出子包：
// import scim "formal.local/w27-4e139ba19e/scim"
```

解析器可一次性函数调用，也可构造固定安全配置的可复用实例：

```go
expr, err := scim.ParseFilter(raw, scim.WithMaxASTDepth(16))
parser := scim.NewParser(scim.WithMaxASTDepth(16), scim.WithMaxNodes(512))
expr, err = parser.Parse(raw)
```

| 文件 | 职责 |
| --- | --- |
| `lexer.go` / `parser.go` | 词法分析与递归下降解析，产出强类型 AST |
| `ast.go` | AST 节点、属性路径、字面量、深度统计、安全序列化与遍历 |
| `eval.go` | JSON 资源求值器（含多值/点分路径/值路径）与租户谓词绑定 |
| `schema.go` | User/Group 属性可变性 schema（`readOnly`/`immutable`/...） |
| `policy.go` | 敏感属性、特权属性/角色、特权组与租户隔离策略 |
| `patch.go` | PATCH path 解析、成员引用提取与 `add`/`remove`/`replace` 应用 |
| `engine.go` | 查询/PATCH 门面与全部授权、可变性、跨租户校验 |
| `store.go` | 带全量快照事务（失败原子回滚）的内存存储 |
| `errors.go` | 哨兵错误、`errors.Is` 映射与 RFC 7644 错误响应体 |

## 过滤器安全用法

```go
engine := scim.NewEngine()
caller := scim.Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}

// 一行完成：深度限制解析 -> 敏感属性授权 -> 绑定 tenantId 隐式谓词
secured, err := engine.SecureFilter(caller, rawFilter)
users, err := engine.List(ctx, caller, "User", rawFilter)
// 最终求值表达式恒为 (用户表达式) and tenantId eq "tenant-a"
```

- 用户输入应通过 `scim.StringLiteral(v)` 构造为 AST 数据，序列化时自动 JSON 转义，引号/`or`/括号无法逃逸为语法。
- `scim.WithMaxASTDepth(n)`、`scim.WithMaxNodes(n)` 与输入长度上限阻断嵌套炸弹。
- `roles`/`groups`/`externalId` 默认是敏感查询属性，需 `identity-auditor`/`identity-admin` 角色或系统管理员；外部 filter 直接引用 `tenantId` 会以 `ErrProtectedAttribute` 拒收。

## PATCH 安全边界

```go
updated, err := engine.ApplyPatch(ctx, caller, "Group", "g-sys", scim.PatchRequest{
    Operations: []scim.PatchOperation{{
        Op: "add", Path: "members",
        Value: []any{scim.Resource{"value": "u-alice"}},
    }},
})
// err 可用 errors.Is 判定：
//   ErrImmutableAttribute / ErrReadOnlyAttribute / ErrUnknownAttribute
//   ErrPrivilegedAttribute / ErrPrivilegedGroup
//   ErrCrossTenantMember / ErrTenantMismatch / ErrNotFound
```

- `id`/`meta`/`groups`/`privileged` 为 `readOnly`，`tenantId` 为 `immutable`，任何 PATCH 均拒绝；schema 未登记属性默认拒绝（防 mass assignment）。
- `roles`/`active`/`externalId` 为特权属性，需 `identity-admin` 或系统管理员。
- `privileged: true`、策略 ID/displayName 命中的组为特权组，非组管理员的任何写操作均拒绝。
- 每个新挂载的 `members.value`/`$ref` 都会解析到实际资源并比对租户；跨租户引用一律返回 `ErrCrossTenantMember`（可用 `Policy.AllowCrossTenantMembers` 关闭，但默认绝不允许）。
- PATCH 在 `Store.Transact` 快照事务中顺序执行；任一操作失败，整个存储恢复到事务前快照，不会留下半写状态。对接真实数据库时把这些校验放进同一事务即可获得相同保证。

## 测试

```bash
go test -race -count=1 ./...
```

测试覆盖：

- `injection_test.go`：恶意值字面量、转义引号逃逸、永真 OR 注入的租户谓词收敛、敏感子句注入、租户键探测、深度炸弹。
- `escalation_test.go`：租户管理员加入系统管理员组、同租户特权组边界、普通组挂载跨租户成员（ID/`$ref`/replace/路径改引用）、`roles` 自授、RFC JSON 信封。
- `patch_test.go`：`tenantId` 不可变、`id`/`groups`/`privileged` 只读、未知属性、特权属性角色、正常增删改路径、跨租户/不可变失败后的原子回滚、存储快照回滚。
- 另有完整解析器、求值器、租户隔离与策略测试。
