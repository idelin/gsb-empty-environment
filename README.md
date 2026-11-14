# EAP-TLS 恢复授权组件

无线控制器为 EAP-TLS 缓存 TLS 票据时，如果票据只按客户端证书摘要关联，访客 SSID 上获得的会话就可能在企业 SSID 上恢复并继承更高 VLAN。本模块提供供 Go AAA 服务导入的恢复授权组件，把一次完整 EAP-TLS 认证的结果和当前网络策略域一起封进 AEAD 票据，恢复时重新校验，而不是相信“同一张证书”。

模块只依赖 Go 标准库（`go 1.24`）。

## 安全模型

初次认证成功后，`Authenticator.Issue` 冻结以下内容并写入 AES-256-GCM 票据：

- 外层 EAP 身份 `OuterEAPIdentity`；
- 服务端虚拟身份 `ServerVirtualIdentity`；
- SSID、BSSID、策略域 `DomainID`；
- 完整客户端证书链指纹、叶证书主体与证书验证策略代次；
- TLS 版本、密码套件；
- 导出的 MSK 身份（稳定的恢复 PSK 标识，不是原始 MSK）；
- 当前证书撤销状态代次；
- 最终 VLAN/ACL 授权，主体始终取自证书，不取外层匿名身份；
- 绝对过期时间。

票据头包含票据 ID、密钥代次、随机 nonce；票据体用 HKDF 派生的一次性密钥和 AEAD 封装。`Keyring.Rotate` 推进代次后，旧代次票据在解密前即被拒绝。

`Authenticator.Resume` 按以下顺序校验，任何失败都不会消费票据：

1. `EarlyData` 为真时直接返回 `ErrEarlyDataNotAuthorized`；`Finished` 为假返回 `ErrHandshakeIncomplete`；
2. 解密票据并检查绝对过期时间；
3. 重新核对当前 AP 的 `DomainID`/SSID/BSSID 是否仍在票据所属策略域；
4. 重新核对当前策略指纹、版本和 VLAN 授权；策略收紧或回滚均使旧票据失效；
5. 重新核对服务端虚拟身份、客户端证书链指纹、证书主体和验证策略代次；
6. 重新查询证书撤销状态，撤销代次变化同样使旧授权失效；
7. 重新核对 TLS 版本/套件、MSK 身份和 channel binding；
8. 全部通过后，才在 `ReplayStore` 中原子消费票据，或消费并轮换出一张新票据。

轮换出的票据使用新 ID，但绝对过期时间不会延长。

## 快速使用

```go
keyring, err := netsec.NewKeyring(masterKey) // 32 bytes
auth, err := netsec.NewAuthenticator(netsec.AuthenticatorConfig{
	Keyring:           keyring,
	ReplayStore:       netsec.NewMemoryReplayStore(),       // 多实例部署请换成共享存储
	RevocationChecker: netsec.NewMemoryRevocationChecker(), // 或 OCSP/CRL 实现
	TrustRoots:        clientCApool,
	MaxTicketLifetime: 8 * time.Hour,
})

issued, err := auth.Issue(ctx, netsec.IssueRequest{
	OuterEAPIdentity:         outerIdentity,
	InnerEAPIdentity:         innerIdentity,
	ServerVirtualIdentity:    "aaa.example.org",
	AccessPoint:              netsec.AccessPoint{DomainID: "guest", SSID: "Guest", BSSID: bssid},
	Policy:                   currentPolicy, // 必须来自 AAA 的权威策略源
	ClientCertificateChain:   chainDER,
	CertValidationGeneration: certPolicyGeneration,
	TLS: netsec.TLSState{
		Version:                tls.VersionTLS13,
		CipherSuite:            tls.TLS_AES_128_GCM_SHA256,
		Finished:               true,
		ChannelBindingType:     "tls-exporter",
		ChannelBinding:         exporter,
		ChannelBindingVerified: true,
		MSKIdentity:            stableMSKIdentity,
	},
	RequestedVLAN: 10,
	ACL:           acl,
	Lifetime:      30 * time.Minute,
})

resumed, err := auth.Resume(ctx, netsec.ResumeRequest{
	Ticket:                   issued.Ticket.Bytes(),
	OuterEAPIdentity:         outerIdentity,
	InnerEAPIdentity:         innerIdentity,
	ServerVirtualIdentity:    "aaa.example.org",
	AccessPoint:              netsec.AccessPoint{DomainID: "guest", SSID: "Guest", BSSID: newBSSID},
	Policy:                   currentPolicy,
	ClientCertificateChain:   chainDER,
	CertValidationGeneration: certPolicyGeneration,
	TLS: netsec.TLSState{
		Version:                tls.VersionTLS13,
		CipherSuite:            tls.TLS_AES_128_GCM_SHA256,
		Resumed:                true,
		Finished:               true,
		ChannelBindingType:     "tls-exporter",
		ChannelBinding:         newExporter,
		ChannelBindingVerified: true,
		MSKIdentity:            stableMSKIdentity,
		PSKIdentity:            issued.Ticket.ID[:], // 当前握手使用的票据 PSK 标识
	},
	Mode: netsec.ResumeRotate,
})
```

## TLS 终止器契约

- 只有收到 TLS `Finished` 后才能把 `TLSState.Finished` 置为 `true`。
- TLS 1.3 0-RTT 数据到来时设置 `EarlyData=true`；本组件不会为它产生授权副作用。
- `ChannelBindingVerified` 必须由终止器在验证 RFC 9266 `tls-exporter`（或部署采用的等价绑定）后设置。
- `MSKIdentity` 是恢复 PSK/MSK 的稳定标识；同一次恢复流程中必须保持一致。
- TLS 1.3 PSK 恢复时 `PSKIdentity` 必须是票据 ID 的原始 16 字节（也接受其十六进制字符串）。
- 同一 TLS 通道上的 802.11r 快速漫游使用 `Resumed=false`，channel binding 必须与票据中冻结值一致；BSSID 可以变化，但必须仍属于同一策略域且在当前策略允许列表中。
- `AccessPoint`、`Policy`、证书链和 TLS 标志必须来自 AAA/RADIUS 侧可信上下文，不能直接采用客户端提交的字段。

## 失败语义

- 跨 SSID/策略域、AP 不在允许列表、策略收紧、证书链变化、验证代次变化、证书撤销、TLS/MSK/channel binding 不匹配、票据过期、密钥代次轮换都会使旧授权失效。
- 所有校验失败都不会消费或轮换票据，因此不会因为一次越权尝试导致合法客户端失去票据。
- 票据重放返回 `ErrTicketReplayed`；并发恢复只有一个调用能成功。
- `MemoryReplayStore` 只适用于单进程或测试；多 AAA 实例必须实现共享的原子 `ReplayStore`。
- `MemoryRevocationChecker` 会为每张证书维护撤销代次；撤销后即使恢复证书，撤销前签发的票据也不会重新生效。

## 验证场景

`go test ./...` 覆盖：

- 同一客户端证书在 Guest/Corp 两个 SSID 分别授权，Guest 票据不能在企业 SSID 恢复；
- 同 SSID 但不同 `DomainID` 不能跨越；
- 同域 BSSID 漫游成功，域外 BSSID/AP 失败；
- 同一 TLS 通道上的合法快速漫游成功，绑定变化失败；
- 撤销后恢复失败，撤销代次永久使旧票据失效；
- TLS 1.3 early data 不消费票据、不产生授权；
- 64 路并发使用同一票据只有 1 个成功；
- 轮换票据不延长绝对过期时间；
- 证书链/验证代次、策略收紧、密钥轮换、票据篡改、匿名外层身份等场景。

运行全部校验：

```sh
go test ./...
go test -race ./...
go vet ./...
```
