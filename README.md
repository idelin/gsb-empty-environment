# reassembly

面向 Go 检测引擎的 TCP 流重组包（`example.org/offlinecli/reassembly`）。

## 用法

```go
pool := reassembly.NewPool(1 << 20) // 全局缺口缓存上限：1 MiB
conn := pool.NewConn(iss + 1)       // 已确认前缀起点

events, unsubscribe := conn.Subscribe(256)
go func() {
    for ev := range events {
        switch ev.Kind {
        case reassembly.EventData: // ev.Seq 处起的一段无缺口确认字节
        case reassembly.EventGap:  // [ev.Seq, ev.End) 已丢失，重置匹配状态
        case reassembly.EventClosed:
        }
    }
}()
defer unsubscribe()

conn.Write(seq, payload) // 任意顺序、可重传、可重叠
conn.Close()
```

## 语义

- **首次到达优先**：确认前缀按先到的字节推进；重传或重叠冲突时保留先到的字节。
- **全局内存限额**：`Pool` 对所有连接的缺口缓存字节数施加单一上限，超限后按最老优先淘汰。
- **精确缺口通知**：被淘汰的区间在确认前缀推进到该处时以 `EventGap{Seq, End}` 精确上报；缺口两侧的字节永远不会拼进同一个 `EventData`，消费者应在缺口处重置检测状态，避免拼出伪造的攻击特征。
- **回绕安全**：序号比较采用 RFC 1982 串号运算，跨 32 位回绕边界正确。

## 测试

```sh
go test -race ./reassembly/
```

覆盖：跨 32 位回绕的重传、部分重叠（先到优先）、淘汰缺口的精确上报、多连接争用全局限额。
