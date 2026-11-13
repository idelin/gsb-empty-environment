# TCP重叠段重组策略差分检测

攻击者发重叠且内容不同的 TCP 段，让检测系统与目标主机各自重组出不同的流，从而绕过规则。为Go入侵检测宿主写可导入的重组包：按目标主机配置的策略（先到优先或后到优先）重组，并在重叠字节不一致时告警，给出冲突区间；重叠内容一致不告警；序号按 32 位回绕比较。用乱序、一致重叠、前后不一致和回绕验证。

## 用法

每个 TCP 流创建一个 `Reassembler`，策略应配置为目标主机实际采用的
重叠处理方式：

```go
r := netsec.NewReassembler(netsec.FirstArrival) // 或 netsec.LastArrival

conflicts := r.Add(seq, payload)
for _, c := range conflicts {
    log.Printf("overlap conflict at [%#x, %#x), %d bytes",
        c.Start, c.End, c.Length)
}
stream := r.Stream()
```

- `FirstArrival`/`FirstComeFirstServed`：先到字节获胜，后到段的非重叠部分仍会补入流。
- `LastArrival`/`LastComeFirstServed`：后到字节覆盖先到字节。
- `Conflict.Start`、`Conflict.End` 为 32 位 TCP 序号，`End` 不含在区间内；跨回绕时 `End` 可为 `0`。同时提供不回绕的 `StartOffset`、`EndOffset` 便于计算。
- 仅内容不同的重叠字节产生告警；内容一致的重复段不告警。相差字节不连续时按连续冲突区间分别返回。
- `Stream()` 返回当前从最小已见序号开始的连续前缀；缺口补齐后自动扩展。
