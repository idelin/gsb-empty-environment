# SD-JWT诱饵与递归

一个可导入的 Go 库，用于验证 SD-JWT 披露结构。它不会只检查第一层 `_sd`，
而是递归处理 payload 和披露值中出现的每一层 `_sd`。

## 使用

验证组件可以直接调用：

```go
claims, err := netsec.Verify(payload, disclosures)
if err != nil {
    // 拒绝该 token
}
```

需要披露/未披露明细时使用：

```go
result, err := netsec.VerifyDetailed(payload, disclosures)
// result.Claims        已合并披露值的 claims
// result.Disclosed     已应用的披露及其 JSON Pointer
// result.Undisclosed   每层仍未披露的摘要及所在 _sd 层
```

另外提供 `VerifyJSON`、`VerifyCompact`（解析 `<jwt>~<disclosure>...`，
不验证 JWT 签名）、`Validate`、`ParseDisclosure`、`DisclosureDigest`
以及 `CreateDisclosure` / `CreateArrayDisclosure`。

## 安全规则

- `_sd_alg` 若存在，只接受精确值 `sha-256`；缺省时按 SD-JWT 规则使用
  `sha-256`。可用 `netsec.WithRequireSDAlg(true)` 强制要求显式声明。
- 摘要必须在整个文档中全局唯一。同一摘要出现在两个 `_sd` 槽位（例如一个
  诱饵槽和一个真实披露槽）会以 `ErrDuplicateDigest` 拒绝。
- 每个披露必须匹配一个槽位；对象属性披露和数组元素披露不能混用。
- 披露不能覆盖 payload 中已有的 claim，也不能创建 `_sd`、`_sd_alg`、`...`
  等保留成员。
- 所有可达的 `_sd` 层都会被处理。未匹配的摘要不会被静默删除，而是保留在
  对应 `_sd` 数组中，并列在 `Result.Undisclosed` 中。

输入 payload 不会被修改。库不负责 JWT 签名验证，验证组件应先校验签名。

## 测试

```sh
go test ./...
go test -race ./...
```

测试包含单层真实披露、非 sha-256 算法、同一摘要同时落在诱饵和披露槽、
两层嵌套 `_sd`，以及每层未披露项保留等场景。
