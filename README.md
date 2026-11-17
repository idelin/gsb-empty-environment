# Modbus网关请求与应答绑定

工业网关把IT侧客户端的Modbus/TCP请求转给串口后的多个从站，攻击者改写Unit ID或功能码去写别的产线线圈，也可抢答他人的事务。给Go网关写供转发循环调用的请求准入包：MBAP协议标识必须为0且长度字段与PDU实际字节一致；功能码和Unit ID按调用方给出的区域策略放行，写类功能码5、6、15、16、23默认拒绝；应答须匹配事务号、Unit ID和功能码（异常应答为功能码加0x80）才交给对应客户端，不匹配丢弃并记录。用长度错位、Unit ID越权、写线圈、事务号冲突和异常应答验证。

## 用法

```go
policy := netsec.Policy{
	Zones: map[uint8]netsec.UnitPolicy{
		1: {Functions: []uint8{1, 2, 3, 4}},
		2: {Functions: []uint8{3, 6}, AllowWrites: true},
	},
}

gate := netsec.NewAdmission(policy, netsec.WithLogger(logger))

// connID 必须唯一标识一个客户端连接；事务号在该连接内绑定。
req, err := gate.AdmitRequest(connID, requestFrame)
if err != nil {
	return err // 已拒绝并记录
}

// ... 将 req 对应请求转发给串口 ...

resp, err := gate.AdmitResponse(connID, responseFrame)
if err != nil {
	return err // 不匹配，已丢弃并记录
}
// 只有匹配的 resp 才交还给该客户端。
```

如果串口应答由集中转发循环处理、调用方事先不知道属于哪个客户端，可使用
`MatchResponse`：

```go
resp, err := gate.MatchResponse(responseFrame)
if err != nil {
	return err // 无匹配或存在跨客户端歧义，已丢弃并记录
}
deliverTo(resp.ClientID, resp)
```

`AllowWrites` 默认为 `false`，因此功能码 5、6、15、16、23 即使出现在
`Functions` 中也会被拒绝；必须在对应区域显式开启写权限。
