# Thread密钥序号切换守卫

攻击者向Thread路由器发带更高密钥序号的消息，想诱导设备切到未知密钥，或回放旧密钥下的帧。给Go协议栈写供帧接收路径调用的密钥状态包：帧内密钥索引=(序号&0x7F)+1；序号只能加1，距上次切换不足调用方保护时间时拒绝；每个密钥序号下对每个邻居的帧计数器须严格递增；已淘汰的序号不得回退启用。用跳号、保护期内切换和旧帧回放验证。

## 使用

```go
state := netsec.NewAt(currentSequence, protection, startupTime)

keyIndex, err := state.AcceptFrame(neighborID, frameSequence, frameCounter, receivedAt)
if err != nil {
    // 丢弃帧：errors.Is 可区分跳号、保护期、旧序号和计数器回放。
    return err
}
// keyIndex 即帧使用的密钥索引：KeyIndex(sequence) = (sequence&0x7F)+1。
// 调用方仍需用 keyIndex 对应的密钥完成帧认证。
```

`AcceptFrame` 只在所有检查通过后更新状态。被拒绝的跳号、保护期内切换或
回放不会推进序号、切换时间或计数器水位。

## 验证

```sh
go test ./...
```
