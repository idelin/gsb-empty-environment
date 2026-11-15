# 隐蔽通道的输出整形

`netsec` 是一个无外部依赖的可导入 Go 包。宿主给定公开的 `Plan{N, L}` 和
`Schedule`，编码器始终输出 N 个长度为 L 字节的 AES-GCM 帧。任务的完成时间只
影响经认证加密后的内容，不改变外部帧数、帧长或发送槽号。

## 使用

```go
plan := netsec.Plan{Slots: 8, SlotBytes: 96}
schedule := netsec.Schedule{Start: start, Interval: time.Second}
key, _ := netsec.NewKey(keyBytes) // 16、24 或 32 字节

enc, err := netsec.NewEncoder(netsec.Config{
    Plan: plan, Schedule: schedule, Round: 17, Key: key,
    Clock: controllableClock,
})
if err != nil { /* plan 或密钥轮次被拒绝 */ }

_ = enc.Submit(secret) // 可在运行期间多次提交
_ = enc.Finish(true)   // 或 Finish(false)；迟到会返回 ErrLate

for {
    frame, err := enc.EmitNext()
    if err != nil { break }
    send(frame.SendAt, frame.Data) // len(frame.Data) == plan.SlotBytes
}

decoded, err := netsec.OpenFrames(plan, 17, key, frames)
```

## 帧格式

- 非末槽明文容量为 `L - 16`。完成前发送零填充；完成后把结果放入最后所需的
  数据槽，首个部分槽右对齐、后续槽填满，因此解码时可直接取数据槽串末尾的
  真实长度字节，不会被中间填充切断。
- 末槽明文前 9 字节是 `uint64` 大端真实长度和 1 字节状态（`0` 失败、`1`
  成功），其余用零填充。
- nonce 恰为 `BE32(round) || BE64(slot)`，每槽唯一。
- AAD 绑定规范域名、轮次、`N`、`L` 和槽号，拒绝换轮次、换计划或换槽重放。
- 同一原始 AES 密钥的 `(key, round)` 在进程内只能声明一次；实例化第二个编码器
  会返回 `ErrRoundReuse`。
- `L < 16 + 9` 的计划被拒绝。由于每槽都带独立的 16 字节 GCM 认证标签，
  `N-1` 个数据槽实际可编码 `(N-1)*(L-16)` 字节；`ResultLimit()` 保留规范中的
  `(N-1)*L` 公开上界，`EncodableBytes()` 给出实际容量。

## 时间与安全边界

截止时间是末槽的公开发送时刻。`Finish` 在该时刻或之后调用会记录失败；末槽发送
时仍无完成报告也会编码为失败。即使任务早完、晚完或报错，宿主仍应调用
`EmitNext` N 次，外部轨迹保持不变。

测试通过可注入 `Clock` 覆盖早完成、晚完成、失败和未完成轨迹。包只声称隐藏认证
密文内容以及固定外部帧轨迹所覆盖的完成时刻，**不**声称隐藏进程调度、CPU/缓存、
内存访问、网络时序或其他加密帧以外的侧信道。
