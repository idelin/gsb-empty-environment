# 状态保护组件

`netsec` 是一个可供 Go 基础设施服务直接导入的状态保护库。它把状态载荷、状态版本、独占锁和备份全部封装为 AES-256-GCM 密文，并把每个密文绑定到工作区、账户、密钥代次、制品角色和版本序号。工作区目录即使对团队可读，文件内容也不会泄漏云凭据。

## 安全属性

- 调用者必须提供经过服务认证的 `Caller.ID`、`WorkspaceID`、`AccountID`；存储实例会同时核对工作区与账户。
- 可选的 `CallerVerifier` 用于校验不透明令牌/会话，并把验证后的身份作为锁与审计的依据。
- 信封不包含明文工作区、账户、版本或载荷元数据；这些信息在 AEAD 关联数据和加密载荷头部中同时绑定。
- 跨工作区复制文件、替换状态文件、交换备份、篡改密文都会在解密或摘要校验时失败关闭。
- 密钥代次由 `KeyProvider` 提供。`Rotate` 会重新加密状态、版本、锁和所有备份；旧代次可在确认完成后删除。
- `ExportState`/`ExportTo` 默认拒绝明文导出；必须配置 `ExportAuthorizer`，推荐使用可过期的签名 `ExportAuthority` 授权。
- 状态文件与导出文件使用 `0600` 权限，锁通过原子发布避免并发覆盖。

## 快速接入

```go
package deploy

import (
	"context"
	"os"

	netsec "example.org/netsec/q0368"
)

func openStore(ctx context.Context) (*netsec.Store, error) {
	// 生产环境应实现持久化代次的 KMS KeyProvider。MemoryKeyring 仅用于
	// 进程内演示/测试，它不会把轮换后的新代次写盘。
	keyring, err := netsec.NewMemoryKeyring([]byte(os.Getenv("STATE_MASTER_KEY")))
	if err != nil {
		return nil, err
	}

	exports, err := netsec.NewExportAuthority([]byte(os.Getenv("EXPORT_AUTHORITY_KEY")))
	if err != nil {
		return nil, err
	}

	return netsec.Open(netsec.Config{
		Root:             "/srv/workspaces/ws-1/state",
		WorkspaceID:      "ws-1",
		AccountID:        "acct-1",
		KeyProvider:      keyring,
		ExportAuthorizer: exports,
		VerifyCaller:     callerVerifier, // 服务自己的令牌/会话校验
		AuditSink:        auditSink,
	})
}
```

调用者身份应由服务的认证层产生，而不是直接信任请求参数：

```go
caller := netsec.Caller{
	ID:          "deployer-42",
	WorkspaceID: "ws-1",
	AccountID:   "acct-1",
	Token:       sessionToken,
}

version, err := store.WriteState(ctx, caller, stateJSON)
stateJSON, err = store.ReadState(ctx, caller)
```

## 密钥轮换

```go
generation, err := store.Rotate(ctx, caller)
if err != nil {
	return err
}

// 所有状态、版本、锁和备份都已使用 generation 重新加密。
// 在确认没有旧代次读取需求后，再删除旧密钥：
if keyring, ok := keyProvider.(*netsec.MemoryKeyring); ok {
	keyring.PruneBefore(generation)
}
```

外部 KMS 可通过实现 `RotatingKeyProvider` 驱动 `Rotate`，或在 KMS 侧创建代次后调用 `RotateTo`。

## 明文导出

默认没有任何导出能力。显式授权流程由独立授权方签发，绑定调用者、工作区、账户、原因、目标路径和有效期：

```go
request := netsec.ExportRequest{
	Reason:      "incident-123",
	Destination: "/srv/exports/ws-1-incident-123.json",
}
grant, err := exportAuthority.Grant(caller, request.Reason, request.Destination, 5*time.Minute)
if err != nil {
	return err
}
request.Authorization = grant

if err := store.ExportTo(ctx, caller, request); err != nil {
	return err
}
```

`ExportTo` 拒绝把明文写入加密状态根目录，并拒绝覆盖符号链接目标。

## 制品布局

默认前缀为 `state`，所有文件均为加密信封：

- `state.state.enc`：当前状态载荷
- `state.version.enc`：加密版本记录与状态摘要
- `state.lock.enc`：加密独占锁
- `state.backup.<version>.enc`：加密历史版本

文件名只暴露制品角色和备份序号，不包含凭据、工作区或账户。

## 验证

测试覆盖合法读写、跨工作区复制后读取、错误密钥、密钥轮换与旧密钥删除、导出授权/过期/重放、版本与备份篡改、锁所有权以及文件明文扫描：

```sh
go test ./...
go test -race ./...
go vet ./...
```
