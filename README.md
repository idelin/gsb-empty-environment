# BACnet/IP 网关写准入

面向转发循环的 BACnet 写准入包。默认策略按以下顺序做 fail-closed 判断：

- 只接受 `WriteProperty` 和 `WritePropertyMultiple`；其他服务（包括读服务）默认拒绝。
- 只放行调用方显式登记的精确 `对象类型 + 实例 + 属性`，不支持通配符。
- 写优先级必须在许可区间内。默认区间为 `3..16`；优先级 `1`、`2` 保留，配置也不允许把它们纳入区间。
- `DeviceCommunicationControl`、`ReinitializeDevice`、`LifeSafetyOperation` 默认拒绝，只有显式调用 `AllowControlService` 才会放行。
- `WritePropertyMultiple` 会校验每条写访问；任意一条对象、属性或优先级违规，整条请求拒绝，并在 `Decision.Violations` 中给出全部违规成员。

## 转发循环调用

```go
policy, err := netsec.NewPolicy(netsec.Config{
	AllowedTargets: []netsec.Target{{
		Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
		Property: netsec.PropertyPresentValue,
	}},
	MinPriority: 5,
	MaxPriority: 10,
})
if err != nil {
	return err
}

// frame 可以是完整 BACnet/IP 帧（BVLC + NPDU + APDU），也可以是裸 APDU。
decision := policy.AdmitAPDU(frame)
if !decision.Allowed {
	// 丢弃并记录 decision.Reason / decision.Member / decision.Violations。
}
```

已解码的请求可直接调用 `policy.Admit(req)`；需要返回 `error` 时使用
`policy.Authorize(req)`、`policy.AuthorizeAPDU(frame)`。

策略可在运行期并发调用 `Register` / `Unregister` 动态维护对象属性登记表。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖 1/2 级与越界优先级、未登记对象/属性、控制服务默认拒绝、
`WritePropertyMultiple` 批量夹带、BACnet/IP 解包以及截断/畸形 APDU。
