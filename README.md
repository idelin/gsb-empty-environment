# 有状态测试复位可靠性

被测安全设备声称reset已完成，但残留计数器会影响下一次试验。实现供Go测试宿主调用的复位一致性包，调用方给初态观察集与探测序列，比较每次复位后的有限行为树；发现差异返回最短探测和两个复位历史。最多深度六，未知响应保留分支。用隐藏持久状态的假设备验证不能只比较一次欢迎响应。

## 用法

包位于 `resetcheck/`。测试宿主实现 `resetcheck.Device`（`Reset`/`Observe`/`Probe`），
行为树有分支时还需实现 `resetcheck.Checkpointer`（`Snapshot`/`Restore`）：

```go
res, err := resetcheck.Check(dev, resetcheck.Config{
    Observations: []string{"welcome"},          // 初态观察集
    Sequences:    [][]string{{"auth", "auth"}}, // 探测序列，单条最长 6
    Resets:       4,                            // 复位次数
})
// res != nil 时：res.Path 为最短探测序列，res.A/res.B 为两个复位历史。
```

运行验证（含隐藏持久状态假设备的回归测试）：

```sh
go test ./resetcheck/
```
