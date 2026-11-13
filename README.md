# Linux密钥环占有者权限与会话继承

Linux 内核密钥环按 32 位权限掩码（占有者、属主、属组、其他各一字节，位含 view、read、write、search、link、setattr）控制访问，进程若与宿主共享会话密钥环，就会通过占有关系读到宿主链入的密钥。为 Go 容器运行时写供密钥操作拦截调用的访问判定包：输入密钥（属主 uid、gid、权限掩码）、调用者凭据与其线程、进程、会话密钥环、各密钥环的链接关系；调用者经线程、进程、会话密钥环沿链接搜索可达该密钥（搜索深度不超过 6）即为占有者，占有者权限与属主或属组或其他中适用的一类权限取并集；沙箱策略要求每个沙箱使用自己新建的匿名会话密钥环，会话密钥环与宿主共享、或沙箱密钥环链入宿主密钥环时拒绝；操作含 view、read、search、link 与 setattr，缺位拒绝。用共享会话密钥环、深度 7 的链、仅属组权限、占有者并集和合规沙箱验证。

## 接入

```go
req := netsec.Request{
	Key: netsec.Key{
		OwnerUID:    1000,
		OwnerGID:    1000,
		Permissions: netsec.PermRead << netsec.PermOwnerShift,
	},
	TargetKeyID: 77,
	Caller: netsec.Caller{
		UID:            1000,
		GID:            1000,
		ThreadKeyring:  10,
		ProcessKeyring: 11,
		SessionKeyring: 12,
	},
	Operation: netsec.OperationRead,
	Graph: netsec.KeyringGraph{
		10: {ID: 10, Keys: []netsec.KeyID{77}},
		11: {ID: 11},
		12: {ID: 12},
	},
	Sandbox: netsec.SandboxPolicy{
		HostSessionKeyring: 1,
		HostKeyrings:       []netsec.KeyringID{1},
		NewAnonymousSession: true,
	},
}

result := netsec.Evaluate(req)
// result.Allowed、result.Possessor、result.Depth、result.Reason
```

`Authorize`/`CanAccess` 返回布尔值，`Check` 在拒绝时返回 `*AccessDeniedError`。密钥环的 `Links` 指向子密钥环，`Keys` 列出直接链入的密钥；搜索从线程、进程、会话三个根开始，取最短可达深度，最大为 6。零值的 `SandboxPolicy` 不启用沙箱检查；启用后要求会话密钥环为新建匿名环，且不能与宿主会话相同或与宿主密钥环互相链接。
