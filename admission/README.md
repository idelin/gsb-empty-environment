# admission — 配置树准入组件

`admission` 包为 Go 服务提供可导入的配置准入能力。平台策略、租户配置与
插件默认值被合成一棵配置树，所有变更以“带来源签名的操作批次”进入准入流程。

## 数据模型

- `Path` 是字符串分量数组（如 `Path{"a.b", "c"}`），分量按原样处理，**不按
  点号拆分**，空分量也合法。
- `Source{Principal, Signature}` 标识一条配置的签名来源；相同字面值若来自
  不同主体，其来源证据不同，不能互换。
- `Literal` 是标量值（nil/bool/number/string），数字保留原始词法文本。
- `Subtree` 是 `OpReplace` 使用的整表片段，每个终端叶子带独立来源。

## 操作

| 操作 | 语义 |
| --- | --- |
| `OpSet` | 在路径写入标量值 |
| `OpLink` | 把路径变成指向另一**绝对路径**的引用 |
| `OpReplace` | 用整表片段替换某路径下的整棵子树 |
| `OpDelete` | 删除叶子或整棵子树（删除不存在的路径为空操作） |

## 准入规则

1. **授权子树**：`Policy.Grants` 给出每个主体可写的路径前缀（祖先授权覆盖
   后代）；`Policy.Denials` 在子路径收紧权限，拒绝优先于授权。
2. **受影响叶子全量核对**：整表替换、删除祖先、改写引用目标时，检查的是
   最终所有受影响的存储终端，而不是命令写下的单个路径。
3. **引用按最终树求值**：多个观察路径可共享同一子图；环与悬空引用一律拒绝。
4. **可信来源闭包**：`Policy.Trusted` 用最长前缀匹配保护某观察路径，该路径
   下每个有效叶子的整条引用链（`EffectiveLeaf.Sources`）都必须来自可信主体。
   间接引用引入的不可信默认值会被拒绝，即使字面值本身无害。
5. **来源可追溯**：每个有效叶子返回字面值、终端来源、引用链 `Chain` 与去重
   后的来源闭包 `Sources`。
6. **限额**：单批次最多 80 个操作、200 个不同路径（引用目标计入）。
7. **原子性**：批次失败时旧树不变，`Apply` 始终返回新快照，不修改接收者。

## 双引擎与交叉校验

- 增量引擎：`Tree.Apply`，基于可变节点树在克隆上应用批次。
- 独立全量解释器：`interpreter.go`，使用完全不同的扁平 `path → entry`
  存储模型、独立的授权/可信/引用解析实现，从当前树快照初始化后重放批次。
- `CrossCheckApply` 同时运行两个引擎，逐叶对比值、终端来源、引用链与来源
  闭包；任何分歧返回 `ErrInterpreterMismatch` 与 `DiffReport`。
- `MergeBatches` 仅当两个批次按两种次序都合法、且逐叶的值与来源证据完全
  相同时才允许合并，否则返回 `ErrMergeConflict`。

## 最小示例

```go
policy := &admission.Policy{
    Grants: map[string][]admission.Path{
        "tenant": {{"tenant"}},
    },
}
policy.AddTrusted(admission.Path{"tenant", "secrets"}, "platform")

tree := admission.NewTree()
next, leaves, _, err := admission.CrossCheckApply(tree, policy, []admission.Op{
    {
        Kind:      admission.OpSet,
        Path:      admission.Path{"tenant", "name"},
        Principal: "tenant",
        Source:    admission.Source{Principal: "tenant", Signature: "sig-1"},
        Literal:   admission.StringLiteral("acme"),
    },
})
```
