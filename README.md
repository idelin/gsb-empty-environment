# Notation 信任策略范围选择

镜像准入使用 Notation 风格的信任策略。`registryScopes` 写错或重叠会让不该被信任的仓库套用宽松策略，因此本包在策略编译和镜像选择两个阶段都做严格校验。

## 选择规则

- scope 支持 `*`、`registry/repo`、`registry/repo/*`（包括 `registry/*`）三种形式。
- 通配符只允许出现在末尾，且只匹配下一级及更深路径：`example.com/prod/*` 匹配 `example.com/prod/app`，不匹配 `example.com/prod`，也不匹配 `example.com/prod-evil/app`。
- 同一镜像命中多条具体 scope 时返回 `ErrOverlappingScopes` 配置错误，即使这些 scope 位于同一个策略中。
- 具体 scope 优先于全局 `*`；只有没有具体 scope 命中时才使用全局策略。
- 镜像的 tag 和 digest 不参与 scope 匹配。

## 验证规则

| 级别 | 过期 | 已吊销 | 吊销未知 |
| --- | --- | --- | --- |
| `strict` | 拒绝 | 拒绝 | 拒绝 |
| `permissive` | 拒绝 | 拒绝 | 放行并记录 |
| `audit` | 记录 | 记录 | 记录 |

签名通过 `Signature.Digest` 绑定镜像摘要，也可同时提供带摘要的 `Signature.Reference`。只有 tag 的签名引用、摘要不一致、签名引用与镜像仓库不一致都会产生 finding；`strict`/`permissive` 下拒绝准入，`audit` 下仅记录。调用方必须提供已解析的镜像摘要；仅凭 tag 无法通过验证。

## 使用

```go
set, err := netsec.ParsePolicySet(policyJSON)
if err != nil {
	// 策略语法错误、非法 scope 或非法验证级别
}

decision, err := set.EvaluateImage(
	"example.com/prod/app:v1",
	resolvedDigest, // sha256:...
	&netsec.Signature{
		Digest:     resolvedDigest,
		ExpiresAt:  expiresAt,
		Revocation: netsec.RevocationValid,
	},
	time.Now(),
)
if err != nil {
	// scope 重叠、无匹配策略或镜像引用非法
}
if !decision.Allowed {
	// 拒绝准入
}
```

`Evaluate`/`EvaluateImage` 用 `Decision.Allowed` 表示准入结果，验证失败不会返回 error；`Verify`/`VerifyImage` 在拒绝时额外返回 `DeniedError`。`audit` 模式始终设置 `Allowed=true`，同时把问题写入 `Decision.Findings`。

## 策略示例

```json
{
  "version": "1.0",
  "trustPolicies": [
    {
      "name": "prod",
      "registryScopes": ["example.com/prod/*"],
      "signatureVerification": {"level": "strict"}
    },
    {
      "name": "global",
      "registryScopes": ["*"],
      "signatureVerification": {"level": "audit"}
    }
  ]
}
```

运行测试：

```sh
go test ./...
```
