# 可导入的对象存储桶策略组件

`netsec` 是一个供 Go 对象存储网关调用的桶策略决策点。它同时处理单对象授权和桶列举，核心约束是：**列举结果必须是同一主体、同一条件上下文下的可读取对象集合**。因此即使策略错误地给出了较宽的 `s3:ListBucket` 权限，网关也不会通过列举泄漏其他租户的对象名。

## 使用方式

```go
policy, err := netsec.NewPolicy([]byte(`
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "TenantAList",
      "Effect": "Allow",
      "Principal": {"AWS": ["tenant-a"]},
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::shared",
      "Condition": {"StringLike": {"s3:prefix": ["tenant-a/*"]}}
    },
    {
      "Sid": "TenantARead",
      "Effect": "Allow",
      "Principal": {"AWS": ["tenant-a"]},
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::shared/tenant-a/*"
    }
  ]
}
`))
if err != nil {
    // 策略无效，拒绝启动或保留旧策略。
}

identity := netsec.Identity{Type: netsec.IdentityUser, ID: "tenant-a", Tenant: "tenant-a"}

// 单对象决策。
decision := policy.Authorize(netsec.Request{
    Action:   netsec.ActionGetObject,
    Bucket:   "shared",
    Key:      "tenant-a/report.pdf",
    Identity: identity,
})
_ = decision.Allowed

// 网关列举：source 是存储层适配器。
source := netsec.NewMemorySource().
    AddKey("shared", "tenant-a/report.pdf").
    AddKey("shared", "tenant-b/secret.pdf")

page, err := policy.List(context.Background(), netsec.ListRequest{
    Bucket:   "shared",
    Prefix:   "",
    MaxKeys:  100,
    Identity: identity,
}, source)
// page.Keys() 只会包含 tenant-a/report.pdf。
```

生产环境的存储层实现 `netsec.ObjectSource`：

```go
type ObjectSource interface {
    // prefix 必须按字面匹配，不得做路径归一化；结果按键升序返回。
    ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]Object, error)
}
```

策略热更新使用 `Manager`。新策略完整编译成功后才会原子生效；编译失败时旧策略继续服务。

```go
manager, err := netsec.NewManager(policyJSON)
if err != nil {
    return err
}
if err := manager.SetPolicy(newPolicyJSON); err != nil {
    // 旧策略仍然有效。
}
result, err := manager.List(ctx, listRequest, source)
```

## 安全语义

- **逐条评估**：每条语句都检查 Effect、Action、Principal、Resource 和 Condition。显式 `Deny` 优先于 `Allow`；没有匹配的 `Allow` 时默认拒绝。
- **列举与读取同主体**：`List` 先要求桶级 `s3:ListBucket`，再对每个候选 key 用同一身份和条件上下文执行 `s3:GetObject`。只有两者都通过的对象名才会返回。只有 `ListBucket` 没有读权限时返回空页；只有读权限没有 `ListBucket` 时列举被拒绝。
- **前缀、分隔符、分页只收窄范围**：列举查询前缀由读权限的固定资源前缀与请求前缀求交；`CommonPrefixes` 只根据已授权的对象生成；分页游标、前缀、分隔符、主体、条件上下文、桶和策略指纹全部绑定在 HMAC 签名的 continuation token 中。篡改或跨范围重放返回 `ErrInvalidContinuationToken`。
- **策略变更**：`Manager` 使用不可变策略快照原子切换。旧策略签发的 continuation token 在策略切换后失效，不会被新策略继续使用。
- **匿名最小权限**：匿名主体只匹配显式的 `"Anonymous"`（或显式的 `"Any"`）主体声明，不会匹配 `{"AWS": "*"}`。匿名读取权限仍必须在逐对象检查中通过。
- **临时凭据最小权限**：临时凭据只匹配显式的 `"Temporary"` 声明，或带明确主体 ID 的认证主体；`{"AWS": "*"}` 不会自动传递给临时凭据。临时凭据的 `ExpiresAt` 过期后所有授权失败。

策略资源使用标准 S3 ARN：

```text
arn:aws:s3:::bucket           桶级动作（如 ListBucket）
arn:aws:s3:::bucket/key       单个对象
arn:aws:s3:::bucket/prefix/*  对象前缀
s3://bucket/prefix/*          等价写法
```

支持的 Principal 形式包括 `{"AWS": ["tenant"]}`、`{"Anonymous": "*"}`、`{"Temporary": ["session-id"]}`、`"*"`（仅认证主体通配符）和原生的 `{"type": "...", "ids": [...]}`。

支持的 Condition 操作符包括 `StringEquals`、`StringLike`、`StringNotEquals`、`StringNotLike`、`Numeric*`、`Bool`、`IpAddress`、`NotIpAddress`、`ArnEquals`、`ArnLike` 及其 `IfExists`、`ForAnyValue:`、`ForAllValues:` 变体。请求上下文自动提供 `s3:prefix`、`s3:delimiter`、`s3:max-keys`、`s3:action`、`s3:bucket`、`s3:key`、`aws:PrincipalType`、`aws:PrincipalAccount`、`aws:PrincipalTenant`、`aws:PrincipalSession`、`aws:PrincipalIsTemporary` 等键。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖：跨租户列举、前缀扩张/回溯前缀、分隔符公共前缀、分页连续性、continuation token 篡改与跨主体重放、合法列举、策略切换后的旧 token 重放、匿名与临时凭据、条件与显式拒绝，以及只返回授权对象名。
