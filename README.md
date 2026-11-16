# BACnet写属性优先级准入

攻击者经BACnet/IP网关以高优先级写Present_Value，或发DeviceCommunicationControl、ReinitializeDevice停设备。给Go网关写供转发循环调用的准入包：只放行调用方登记的对象与属性，优先级须在许可区间（1、2级保留），控制类服务默认拒绝，WritePropertyMultiple任一项违规整条拒绝。用越权优先级、批量夹带验证。

## 用法

```go
policy, err := netsec.NewPolicy([]netsec.Rule{
	{
		Object:   netsec.ObjectID{Type: netsec.ObjectAnalogInput, Instance: 0},
		Property: netsec.PropertyPresentValue,
	},
}, netsec.WithPriorityRange(8, 16))
if err != nil {
	return err
}

req := netsec.Request{
	Service: netsec.ServiceWriteProperty,
	Writes: []netsec.Write{{
		Object:   netsec.ObjectID{Type: netsec.ObjectAnalogInput, Instance: 0},
		Property: netsec.PropertyPresentValue,
		Priority: 8,
		Value:    float32(21.5),
	}},
}
if err := policy.Admit(req); err != nil {
	// Drop the whole request; never partially forward or apply it.
	return err
}
```

`Admit` 只接受已解码的 `WriteProperty` / `WritePropertyMultiple`。默认优先级区间为
3..16，配置无法把 1、2 级纳入区间；缺少优先级、1/2 级和越界优先级均拒绝。
`DeviceCommunicationControl`、`ReinitializeDevice` 以及其他未显式支持的 confirmed
service 一律拒绝。`WritePropertyMultiple` 会逐项检查，任一项对象、属性、优先级或
结构违规时整条请求返回错误。策略构造后不可变，可被转发循环并发调用。

网关必须只解析一次请求并将同一份 `Writes` 交给准入和转发，不能在准入后重新解析
原始 APDU，否则会产生解析差异。
