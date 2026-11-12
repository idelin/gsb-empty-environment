# OCI引用列表投毒过滤

镜像仓库对同一subject返回的referrers列表可被推入伪造签名或证明。为Go验证客户端实现可导入的过滤包：只留artifactType在许可表内的描述符，逐项取回manifest，核对subject确为被查摘要且内容摘要与描述符一致；回退标签sha256-<hex>索引同样处理。用伪subject、被换内容和重复摘要样例测试，被滤除项不得进入验签。

## 包位置与导入

实现位于 `referrerfilter` 子包（仅依赖标准库）：

```go
import "example.org/netsec/q0159/referrerfilter"
```

## 信任闸门

`referrerfilter.FilterAndVerify` 对仓库返回的每个描述符依次执行：

1. **许可表**：`artifactType` 必须在 `NewAllowList(...)` 显式列出，未命中项在任何网络请求之前即被拒绝。
2. **摘要合法性**：描述符 digest 必须是合法的 `sha256`/`sha512` 算法与十六进制长度。
3. **内容完整性**：拉取 manifest 后按描述符算法重算摘要，必须与描述符 digest 完全一致；`size` 不一致同样拒绝（防内容被换）。
4. **subject 绑定**：解析 manifest 的 `subject.digest`，必须等于被查询摘要；缺失或指向其他 subject 一律拒绝（防伪造 subject）。
5. **去重**：相同 digest 只拉取、只验签一次，重复项以 `duplicate_digest` 拒绝。

只有全部通过的描述符才会调用注入的 `Verifier`，被滤除项保证不进入验签。拒绝项通过 `Result.Rejected` 的稳定 `Reason` 上报，可直接用于指标与审计。

## 回退标签

当 Referrers API 返回 404（映射为 `ErrReferrersUnsupported`）时，自动按 Distribution 规范拉取 `sha256-<hex>` 标签，要求其为 OCI image index，再对其中的描述符执行完全相同的闸门；`Result.FallbackUsed` 报告是否走了回退路径。

## 使用示例

```go
registry := &referrerfilter.HTTPRegistry{
    BaseURL: "https://registry.example.com",
    Header:  http.Header{"Authorization": []string{"Bearer " + token}},
}
allowed := referrerfilter.NewAllowList(
    "application/vnd.example.signature.v1+json",
    "application/vnd.example.attestation.v1+json",
)
verifier := referrerfilter.VerifierFunc(func(ctx context.Context, d referrerfilter.Descriptor, raw []byte) error {
    // 此处的 d 与 raw 已通过许可表、摘要与 subject 全部检查
    return verifySignature(raw)
})
result, err := referrerfilter.FilterAndVerify(
    ctx, registry, "example/app", subjectDigest, allowed, verifier,
)
```

## 测试

`go test ./...` 覆盖：许可表外类型、伪 subject、缺失 subject、被换内容（摘要不符）、size 不符、重复摘要、清单拉取失败、非法 JSON、验签失败、Referrers API 与 `sha256-<hex>` 回退两条链路，以及基于 `http.RoundTripper` 的端到端 HTTP 行为（含 404/500 映射）。
