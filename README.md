# audit — 结构化快照审计比较库

比较两份 JSON 风格快照（`map[string]interface{}` / `[]interface{}` / 标量），
按路径规则审计变更，输出**最浅违规路径**及其旧值、新值。

## 规则

| 规则 | 含义 |
| --- | --- |
| `Immutable` | 不可变路径：命中路径的整棵子树不得有任何增删改 |
| `AllowAdd` | 允许新增路径：新增子树的根须精确命中某条模式 |
| `AllowRemove` | 允许删除路径：删除子树的根须精确命中某条模式 |

- 已存在标量的**修改**默认允许，除非被 `Immutable` 覆盖；只有成员的**新增/删除**需要 allow 规则。
- 路径模式：`$.spec.containers[*].image`；`*`（或 `[*]`）**只匹配单层**（一个键或一个下标），`""`/`"$"` 表示根。
- **父对象替换/删除仍需递归检查受保护后代**：父节点被整体替换或删除时，会逐叶展开子树，
  命中 `Immutable` 的后代路径若发生变化仍报违规——即使该替换/删除本身被规则允许。
- 违规收敛：同路径去重（immutable > remove > add），被祖先违规覆盖的丢弃，按深度再字典序输出最浅集合。

## 用法

```go
vs, err := audit.Compare(oldV, newV, audit.Rules{
    Immutable:   []string{"$.metadata.name", "$.spec.replicas"},
    AllowAdd:    []string{"$.metadata.labels", "$.spec.containers[*].env"},
    AllowRemove: []string{"$.metadata.annotations"},
})
// vs == nil 表示合规；否则每个 Violation 含 Path / Kind / Old / New（缺失侧为 audit.Absent）
```

也可 `audit.Compile(rules)` 复用编译后的 `Checker`，或用 `audit.CompareJSON` 直接比较 JSON 字节。

## 测试

```
go test ./...
```

- `audit/audit_test.go`：各规则的定向用例（通配单层、父对象替换/删除的受保护后代、最浅收敛等）。
- `audit/reference_test.go`：独立的**逐叶规则展开**参考实现，对随机快照与随机规则做 5000 次交叉核对，
  并验证输出满足"无祖先覆盖、最浅优先"的不变量。
