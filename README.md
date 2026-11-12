# MQTT-SN主题编号映射隔离

MQTT-SN网关把UDP客户端的主题编号映射成MQTT主题，攻击者在REGISTER里写通配符主题、复用他人的预定义编号，或沿用清理会话前的旧编号。给Go网关写供数据报处理调用的主题映射包：编号表按客户端隔离，主题名含+或#拒绝，预定义编号只能引用调用方白名单，会话清理后编号全部作废，2字节短主题名按原样转发。用通配注册、跨客户端编号和旧编号验证。

## topicmap 包

`topicmap` 实现数据报处理层调用的主题编号映射，覆盖 MQTT-SN 的三段 16 位编号空间：

- `0x0000`：短主题名，两个字节本身即主题，由 `ShortTopicName` 解码后原样转发；
- `0x0001..0xFF00`：REGISTER 分配的普通编号，按客户端隔离存储；
- `0xFF01..0xFFFF`：预定义编号，只接受调用方白名单中的条目。

### 安全语义

- REGISTER 的主题名含 `+` 或 `#` 时返回 `ErrWildcardTopic`，不写入任何映射；
- 普通编号表以客户端标识（如 UDP 远端地址）为键，其他客户端解析得到 `ErrUnknownTopicID`；
- 预定义编号通过 `PredefinedWhitelist` 按客户端授权，越权引用返回 `ErrPredefinedNotAllowed`；
- `CleanSession` 删除该客户端全部普通编号，重放旧编号无法解析；白名单属于网关配置，不受影响；
- 所有方法在 `sync.RWMutex` 下保护，可被并发的数据报 goroutine 调用。

### 数据报处理示例

```go
table, err := topicmap.New(topicmap.PredefinedWhitelist{
    clientKey: {topicmap.MinPredefinedID: "alerts/temp"},
})
if err != nil { /* 白名单含通配符或编号越界 */ }

switch id := msg.TopicID; {
case id == topicmap.ShortTopicID:
    mqttTopic = topicmap.ShortTopicName(id) // 2 字节短主题，原样转发
case id >= topicmap.MinPredefinedID:
    mqttTopic, err = table.Resolve(clientKey, id) // 校验调用方白名单
default:
    mqttTopic, err = table.Resolve(clientKey, id) // 仅本客户端 REGISTER 的编号
}

// REGISTER：通配符主题在此被拒
ackID, err := table.Register(clientKey, msg.TopicName)

// CONNECT clean session = 1：作废该客户端所有普通编号
table.CleanSession(clientKey)
```

### 验证

`go test -race ./...` 覆盖通配注册拒绝、跨客户端编号隔离、清理后旧编号失效、预定义白名单越权、短主题编解码和并发访问。
