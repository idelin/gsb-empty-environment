# 跨租户代办角色授权包

`netsec` 是给 Go 凭据宿主使用的可导入包。它解决的核心问题是：备份平台可以用同一个平台主体代表多个客户调用云角色，但授权必须绑定到“租户”，不能只绑定到目标角色。否则客户 A 可能诱导平台代入客户 B 的角色。

## 安全不变量

- 租户登记记录包含 `tenantID`、精确 `roleID`、随机 128 位 `externalID`、允许的 `sourceResource` 集合、最大会话秒数、策略摘要和可选 audience。
- 请求者身份只来自宿主认证层构造的 `AuthContext`；`AssumeRequest` 没有任何 principal 字段，无法从请求体伪造主体。
- 一次请求只有在以下条件全部成立时才会触达外部 STS：
  - 请求的 `tenantID` 属于认证主体；
  - `roleID` 与登记值精确相等；
  - `externalID` 与登记值常量时间比较相等；
  - `sourceResource` 在登记集合中，且当前资源所有者仍是该租户；
  - `sessionName` 是 1 到 64 字节的安全 ASCII。
- `sessionName` 只写入审计记录，不进入授权决定摘要，也不参与任何策略匹配。它不会决定租户、角色或资源是否被允许。
- 请求的 `duration` 会被 `maxSessionSeconds` 截断；STS 返回的过期时刻必须晚于当前时间且不晚于该上限。
- STS 返回的 `audience`、`roleID`、`sourceIdentity`、`sessionID` 必须与决定完全一致，过期时刻必须满足上限。任何不一致都会立刻调用 `STSCredential.Destroy`，错误不会携带交付凭据。
- 相同认证主体和 `requestID` 的并发请求只产生一次 STS 调用，并共享同一个决定和凭据。相同 `requestID` 携带不同参数会返回 `ErrRequestConflict`。
- 每次租户登记更新、资源所有权转移都会推进配置代号。STS 回调期间若代号变化，结果会被销毁并返回 `ErrStaleConfiguration`；已缓存但代已过期的凭据也不会再次发布。
- 交付的 `IssuedCredential` 带有 `DecisionID`、`TenantID`、`PrincipalID`、`TenantGeneration` 和 `PolicyDigest`，可通过 `Broker.DecisionByID` 回溯到唯一决定。

## 使用方式

```go
registry := netsec.NewRegistry()
externalID, err := netsec.GenerateExternalID()
if err != nil {
    return err
}
if err := registry.RegisterTenant(netsec.TenantRecord{
    TenantID:          "tenant-a",
    RoleID:            "arn:aws:iam::111111111111:role/backup-a",
    ExternalID:        externalID,
    SourceResources:   []string{"arn:aws:s3:::tenant-a-backup"},
    MaxSessionSeconds: 3600,
    PolicyDigest:      "sha256:tenant-a-policy-v1",
}); err != nil {
    return err
}

broker := netsec.NewBroker(registry, stsCallback)

// auth 必须由宿主认证上下文构造，绝不能从 HTTP 请求体读取。
auth := netsec.AuthContext{
    PrincipalID: "platform-principal",
    TenantIDs:   []string{"tenant-a"},
}

credential, err := broker.Assume(ctx, auth, netsec.AssumeRequest{
    RequestID:        "request-123",
    TenantID:         "tenant-a",
    RoleID:           "arn:aws:iam::111111111111:role/backup-a",
    ExternalID:       externalID,
    SourceResource:   "arn:aws:s3:::tenant-a-backup",
    SessionName:      "backup-2026-11-14",
    DurationSeconds:  1800,
    ParametersDigest: "sha256:canonical-call-parameters",
})
```

`stsCallback` 实现：

```go
type STS interface {
    AssumeRole(context.Context, netsec.STSRequest) (*netsec.STSCredential, error)
}
```

回调应把 `STSRequest` 中的 `Audience`、`SourceIdentity`、`SessionID`、`RoleID` 原样绑定到返回凭据，并保证 `Expiration` 不晚于 `NotAfter`。

## 决定摘要

`Decision.Digest` 是对固定顺序、长度前缀编码的 SHA-256。编码包含：

- 请求中的 `tenantID`、`roleID`、`externalID`、`sourceResource`、`duration`、调用参数摘要和 `requestID`；
- 认证主体 `principalID`；
- 租户策略摘要、配置代号；
- 决定的 audience、source identity、随机 session ID；
- 授权结果和稳定拒绝码。

`Decision.RecomputeDigest` 可用于独立核验审计记录。`sessionName` 只保存在 `Decision.SessionName` 审计字段中，既不会进入摘要，也不会参与策略判断。

`Decision` 会保留登记的外部 ID 等敏感审计材料，应只存放在受保护的审计存储中，不能原样返回给请求者。

## 并发与换代

- `requestID` 按 `(principalID, requestID)` 作用域去重，避免一个主体拿到另一个主体缓存的凭据。
- 成功结果会保留并复用；重放相同命令返回相同 `DecisionID` 和凭据内容。
- 租户登记更新、删除或资源所有权转移都会推进相关租户代号。回调返回后会在发布前原子复核代号；旧代号的结果立即销毁。
- `Broker.Close` 会销毁缓存凭据；关闭后不再发布任何结果。

## 测试

测试覆盖两个租户共享同一平台主体时的换角色、换 external ID、资源归属转移、迟到回调、并发重试、回调字段篡改、过期上限、凭据销毁和凭据回溯。

```sh
go test ./...
go test -race ./...
```
