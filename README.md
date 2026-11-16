# 扩展公钥与子私钥的恢复闭包

分层确定性钱包允许导出扩展公钥给对账或监控服务，但只要攻击者再拿到该公钥下任意一个非硬化子私钥，就能反推出父私钥，进而得到整棵子树。本包 `netsec` 提供可导入的暴露审查模型：输入钱包树与已导出材料，输出全部暴露私钥路径及每项暴露的一条推导链；新增导出若扩大暴露集合，则整批拒绝。

模块路径：`example.org/netsec/q0825`

## 模型

- `Path`：绝对派生路径，例如 `m/44'/0'/0'/0/7`；支持 `'`、`h`、`H` 硬化标记。
- `Tree`：钱包节点集合。只需给出关心的节点路径，缺失祖先会自动补齐；父子和硬化关系由路径决定。
- `Material`：一条导出材料，类型为 `xpub`、`xprv` 或 `priv`，各带一个树内路径。`xpub` 视为包含链码，`xprv` 视为包含私钥和链码。

## 暴露规则

1. 导出 `xprv` 或 `priv`：该节点私钥暴露，且所有后代私钥暴露；硬化后代同样暴露。
2. 导出 `xpub`：本身不暴露私钥，但可沿非硬化边公开派生出后代 `xpub`。
3. 持有节点 `P` 的 `xpub` 与任一非硬化子节点 `C` 的私钥：恢复 `P` 的私钥。
4. 恢复出的私钥同样使其全部后代私钥暴露。
5. 向上传导逐级进行：若 `P` 又是其父节点 `Q` 的非硬化子节点，且 `Q` 的 `xpub` 可用，则继续恢复 `Q`。`Q` 的 `xpub` 可以显式导出，也可以由更高层导出的 `xpub` 沿非硬化路径公开派生。
6. 硬化边同时阻断公开子钥派生和子私钥反推父私钥；父 `xpub` 也不能得到硬化子私钥。

审计结果按路径确定性排序，每项包含 `path` 和 `chain`。`chain` 中的规则包括：

- `exported_xpub`
- `derived_xpub_non_hardened`
- `exported_xprv`
- `exported_private_key`
- `derived_descendants_from_private_key`
- `recovered_parent_from_xpub_and_non_hardened_child_private_key`

## 使用

```go
tree, err := netsec.NewTreeFromStrings([]string{
    "m/44'/0'/0'/0/0",
    "m/44'/0'/0'/0/1",
    "m/44'/0'/0'/1/0",
})
if err != nil {
    log.Fatal(err)
}

report, err := netsec.AuditExports(tree, []netsec.Material{
    {Kind: netsec.ExtendedPublicKey, Path: netsec.MustParsePath("m/44'/0'/0'")},
    {Kind: netsec.PrivateKey, Path: netsec.MustParsePath("m/44'/0'/0'/0/0")},
})
if err != nil {
    log.Fatal(err)
}
for _, exposure := range report.Exposed {
    fmt.Println(exposure.Path, exposure.Chain)
}
```

也可以直接从 JSON 导入材料。JSON 接受数组，或带 `materials` 字段的对象：

```json
{
  "materials": [
    {"kind": "xpub", "path": "m/44'/0'/0'"},
    {"kind": "priv", "path": "m/44'/0'/0'/0/0"}
  ]
}
```

```go
materials, err := netsec.ReadMaterials(reader)
```

`kind` 同时接受 `extended_public_key`、`extended_private_key`、`leaf_private_key` 等别名。

## 增量导出审查

```go
review, err := netsec.ReviewAddition(tree, existing, additions)
if err != nil {
    log.Fatal(err)
}
if !review.Accepted {
    // 整批拒绝；review.NewlyExposed 给出新增暴露及推导链。
}
```

审查是严格的全有或全无策略：先审计 `existing`，再审计 `existing + additions`；只要有任何基线中安全的路径在提案中变为暴露，`Accepted` 即为 `false`。因此，新增 `xpub` 可能因已有子私钥而触发拒绝；新增 `xprv` 或 `priv` 也通常会因其自身或后代新暴露而被拒绝，除非该路径在基线中已经暴露。

## 验证

```sh
go test ./...
go vet ./...
```

测试覆盖：`xpub` 加非硬化子私钥、硬化子私钥不可反推、逐级向上传导、硬化边阻断、仅导出 `xpub`、私钥后代暴露，以及增量导出拒绝。
