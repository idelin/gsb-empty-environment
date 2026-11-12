# CTAPHID通道抢占防护

攻击者在FIDO认证器交互中途发INIT抢占通道，或乱序续包扰乱组装。给Go固件写供HID回调调用的CTAPHID组装包：广播通道0xFFFFFFFF只收8字节随机数的INIT；消息未完成时其他通道回ERR_CHANNEL_BUSY，续包序号错回ERR_INVALID_SEQ，500ms超时回ERR_MSG_TIMEOUT并丢弃；BCNT超过7609拒绝。用抢占INIT、乱序续包和超时验证。

## 用法

`ctaphid.go` 提供 HID 回调可直接调用的 `Assembler`：

```go
a := netsec.NewAssembler(func(cid uint32, cmd byte, payload []byte) ([]byte, byte) {
    // 处理组装完成的 CTAPHID 消息，返回响应载荷与错误码（0 表示成功）。
    return response, 0
})

// HID 输入报告回调：64 字节报告进，零到多个输出报告出。
for _, out := range a.HandleReport(report) {
    hidInterruptOut(out)
}

// 固件定时器周期调用，驱动 500ms 事务超时。
for _, out := range a.Tick() {
    hidInterruptOut(out)
}
```

## 安全规则

- 广播通道 `0xFFFFFFFF` 仅接受 `BCNT=8` 的 INIT（8 字节随机数），其它命令或长度分别回 `ERR_INVALID_CMD` / `ERR_INVALID_LEN`。
- 任一通道存在未完成事务时，其它通道（含广播）的 INIT 回 `ERR_CHANNEL_BUSY`，不破坏原事务；同通道重发 INIT 则终止旧事务并重建。
- 续包序号必须从 0 严格递增，乱序或重复回 `ERR_INVALID_SEQ` 并丢弃整个事务。
- 每个包刷新 500ms 空闲截止时间，超时由 `Tick` 或下一次报告触发，回 `ERR_MSG_TIMEOUT` 并丢弃事务。
- `BCNT > 7609` 回 `ERR_INVALID_LEN` 拒绝；未知单播 CID 回 `ERR_INVALID_CHANNEL`。

## 验证

`go test -race ./...` 覆盖抢占 INIT、乱序续包、500ms 超时（Tick 与迟到续包两条路径）、BCNT 上限、同通道重建、完整重组分包及并发回调。
