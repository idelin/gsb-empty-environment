# SLSA构建器身份比对

提交者可控的构建参数能让产物证明冒用受信构建器。为Go准入服务实现可导入的SLSA v1 provenance比对包：builder.id须精确命中受信表且buildType一致，externalParameters只许登记键，出现internalParameters即拒，依赖摘要须含声明源提交。用多余参数、同前缀builder.id和缺摘要样例测试。

## 用法

包路径为 `example.org/netsec/q0191`，核心入口是 `Verify`、`Compare` 和
`VerifyJSON`。所有拒绝原因都是可包装的哨兵错误，可用 `errors.Is` 分类：

```go
policy := netsec.Policy{
    TrustedBuilders: map[string]string{
        "https://build.example.com/slsa": "https://build.example.com/slsa/v1",
    },
    AllowedExternalParameters: []string{"source", "branch"},
    SourceCommit:              "0123456789abcdef0123456789abcdef01234567",
    SourceRepository:          "github.com/example/project",
}

if err := netsec.VerifyJSON(statementJSON, policy); err != nil {
    switch {
    case errors.Is(err, netsec.ErrUntrustedBuilder):
        // builder.id 未精确命中受信表
    case errors.Is(err, netsec.ErrInternalParametersPresent):
        // 提交者试图提供构建器内部参数
    default:
        // 拒绝并记录 err
    }
}
```

## 校验规则

- `runDetails.builder.id` 必须与 `TrustedBuilders` 的键逐字节相等，不做前缀或规范化匹配。
- `buildDefinition.buildType` 必须等于该受信构建器绑定的构建类型。
- `buildDefinition.externalParameters` 必须是 JSON 对象，且所有顶层键都在登记白名单内。
- 只要出现 `buildDefinition.internalParameters` 字段就拒绝，包括 `{}` 和 `null`。
- 至少一个 `resolvedDependencies` 的摘要值必须精确等于 `SourceCommit`；声明了 `SourceRepository` 时，该依赖 URI 还必须匹配仓库。
