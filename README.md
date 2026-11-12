# DNP3冷热重启区分准入

攻击者伪造DNP3冷重启(FC 13)或热重启(FC 14)请求，或把控制操作拆进多个传输层分片绕过检查。本包给Go安全网关提供应用层请求准入：先按FIR/FIN重组再检查功能码；冷、热重启分开授权，默认仅维护主站地址可发；选择(3)与操作(4)须同一主站、序号一致且在调用方超时内；未知功能码拒绝。

## 使用

```go
clock := time.Now
gateway := netsec.New(netsec.Policy{
    MaintenanceMasters: []uint16{0x0001},
    ColdRestartMasters: []uint16{0x0001},
    HotRestartMasters:  []uint16{0x0002},
    SelectTimeout:      500 * time.Millisecond,
    Clock:              clock,
})

decision := gateway.Admit(netsec.Request{
    Source:      0x0001,
    Destination: 0x000A,
    Fragments:   fragments, // 同一传输消息的 FIR/FIN 分片
})
if !decision.Allowed {
    // 拒绝转发；decision.Reason 可用于审计。
}
```

`TransportFragment` 是去掉链路层头后的传输帧：`FIR`/`FIN` 来自传输控制字节，`Sequence` 为6位传输序号，`Payload` 为应用层字节。`ParseTransportFragment` 可直接解码以传输控制字节开头的原始帧。

## 准入顺序

1. 验证分片顺序和传输序号，按FIR/FIN重组；不完整消息直接拒绝，不缓存半个请求。
2. 校验应用控制字节的FIR/FIN，解析应用功能码和应用序号。
3. 未知功能码一律拒绝；可选的 `AllowedFunctionCodes` 再限制已知功能码。
4. FC 13 与 FC 14 分别检查冷、热重启授权。专用列表为空时回退到 `MaintenanceMasters`；维护列表也为空时仅 `DefaultMaintenanceMaster`（地址0）可发。
5. SELECT(3) 记录 `(主站, 目标站, 应用序号)`；OPERATE(4) 必须精确匹配、未被消费且未超过 `min(Request.Timeout, Request.Deadline, Policy.SelectTimeout)`。成功后 SELECT 被消费，重放失败。

明确规定：本包按任务要求将 FC 13 解释为冷重启、FC 14 解释为热重启。地址是调用方提供的链路层/应用层地址，网关部署仍需在可信链路层完成源地址认证，防止地址伪造。
