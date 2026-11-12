# vsock对端CID代次绑定

宿主机上的vsock代理按对端CID判断请求来自哪台虚拟机，攻击者所在虚拟机销毁后CID被分给新租户，旧连接和旧映射仍然有效。给Go代理写供Accept回调调用的CID绑定包：每个CID映射到带代次的虚拟机实例，源CID为0到2的保留值拒绝，端口只能访问该实例允许的集合；实例销毁或CID重新分配时关闭并作废该代次的全部连接，迟到的回调按旧代次丢弃。用CID重用、保留源地址、端口越权和并发销毁验证。

## 用法

`Registry` 把每个源 CID 映射到一个带代次（generation）的虚拟机实例租约，供 vsock 代理的 Accept 回调调用。

```go
registry := netsec.NewRegistry()

// 管控面：CID 42 租给实例 vm-a，仅允许访问 8080/9090。
token, err := registry.Register(42, "vm-a", []uint32{8080, 9090})

// Accept 回调：按当前代次校验源 CID 与目标端口。
bound, err := registry.Bind(remoteCID, dstPort, rawConn)
if err != nil { // ErrReservedCID / ErrUnknownCID / ErrPortNotAllowed
	return // 拒绝时 rawConn 已被关闭，不会泄漏
}
defer bound.Close()

// 异步回调若持有注册时的 Token，可在 CID 被重新分配后识别旧代次。
bound, err = registry.BindToken(token, dstPort, rawConn) // 旧代次返回 ErrStaleGeneration

// 实例销毁：关闭并作废该 CID 当前代次的全部连接。
registry.Revoke(42)

// CID 重新分配给新租户：旧代次连接全部关闭，代次号递增。
newToken, _ := registry.Register(42, "vm-b", []uint32{2002})
```

## 安全语义

- 源 CID 0..2 为保留值（任意/hypervisor/host），注册与绑定一律拒绝。
- 端口必须命中实例允许集合，越权访问返回 `ErrPortNotAllowed` 并关闭连接。
- 每次 `Register` 生成新代次；CID 重用时旧代次连接被关闭，迟到的 `BindToken` 回调按 `ErrStaleGeneration` 丢弃。
- `Revoke` 销毁实例并关闭其全部连接；之后的 `Bind` 返回 `ErrUnknownCID`。
- 所有拒绝路径都会关闭传入的 `net.Conn`，避免接受的套接字泄漏。
