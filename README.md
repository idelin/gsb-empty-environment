# credentialhost

多租户备份平台的 Go 代办凭据授权包。平台主体可以代表多个客户，但每次
Assume 只绑定一个租户登记代际，避免客户 A 借平台身份代入客户 B 的角色。

## 安全模型

- 租户登记固定 `TenantID`、精确 `RoleID`、随机 128 位 `ExternalID`、允许的
  `SourceResource` 集合、最大会话秒数和策略摘要。
- 请求者主体只能来自宿主认证上下文
  `credentialhost.WithPrincipal`，`AssumeRequest` 不包含主体字段。
- 授权摘要规范覆盖主体及六项：`tenantID`、`roleID`、`externalID`、
  `sourceResource`、`duration`、`parametersDigest`。
- `sessionName` 必须为 1 至 64 字节安全 ASCII（字母数字及 `+=,.@_-`），只进入
  审计和请求指纹，不进入授权决定摘要。
- 只有租户属于主体、角色精确相等、外部 ID 常量时间匹配、资源在允许集合内，
  且资源当前仍归该租户所有时，才调用外部 STS 回调。
- STS 返回的 audience、roleID、sourceIdentity、sessionID 和过期时刻必须与
  决定一致，且过期不晚于请求上限；不一致或已过期时立即 `Destroy`。
- 相同 `RequestID` 与参数并发/重放只调用一次 STS；相同 ID 参数不同返回
  `ErrRequestIDConflict`。
- 租户撤销、租户配置换代或资源归属转移后，旧缓存和在途回调结果均返回
  `ErrStaleRequest`；未交付的迟到凭据立即销毁，不能通过旧 `RequestID` 复用。

## 用法

```go
registry := credentialhost.NewRegistry()
record, err := registry.RegisterTenant(credentialhost.TenantConfig{
	TenantID:          "tenant-a",
	RoleID:            "arn:aws:iam::111111111111:role/backup",
	SourceResources:   []string{"arn:aws:s3:::tenant-a-bucket"},
	MaxSessionSeconds: 3600,
	PolicyDigest:      "sha256:...",
})
if err != nil {
	// handle error
}

host, err := credentialhost.NewHost(registry, sts)
if err != nil {
	// handle error
}

ctx := credentialhost.WithPrincipal(ctx, credentialhost.Principal{
	Subject:   "platform-service-principal",
	TenantIDs: []string{"tenant-a", "tenant-b"},
})

credentials, err := host.Assume(ctx, credentialhost.AssumeRequest{
	RequestID:        "request-123",
	TenantID:         "tenant-a",
	RoleID:           record.RoleID,
	ExternalID:       record.ExternalID,
	SourceResource:   "arn:aws:s3:::tenant-a-bucket",
	SessionName:      "backup-job-123",
	DurationSeconds:  900,
	ParametersDigest: "sha256:...",
})
```

外部 STS 实现 `credentialhost.STS`，从 `STSRequest.Decision` 获取本次授权的
预期 `Audience`、`SourceIdentity`、`SessionID` 和 `NotAfter`，并回传匹配的
`Credentials`。校验通过后宿主会写入 `DecisionDigest`、`TenantID` 和代际版本，
从而可把每份交付凭据回溯到唯一决定。如需撤销底层凭据，在
`Credentials.DestroyFunc` 中实现。

`Registry.ReplaceTenant` 生成新一代配置和全新外部 ID；
`Registry.RevokeTenant` 永久撤销租户；`Registry.SetResourceOwner` 用于资源
归属转移。

## 验证

```sh
go test ./...
go test -race ./...
```
