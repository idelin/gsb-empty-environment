# RESP流水线重编码边界

分片代理把客户端的RESP流水线转给后端Redis，攻击者在批量字符串里夹带换行和下一条命令，或借多字节字符让长度前缀与实际字节数不一致。这个包解析数组式和内联请求，只放行调用方配置的命令名（忽略大小写），转发时一律按实际字节长度重新生成批量字符串；被拒绝的请求不发往后端，而是为原流水线位置返回固定的错误应答。

## 转发循环用法

```go
filter, err := netsec.NewFilter([]string{"GET", "MGET"}, netsec.WithMaxDepth(8))
if err != nil {
    return err
}
stream := filter.NewStream()

// chunk 来自客户端连接，长度任意。
frames, err := stream.Feed(chunk)
for _, frame := range frames {
    if frame.Forwarded {
        backend.Write(frame.Request) // 只发送规范化后的批量字符串数组
        slots = append(slots, backendSlot{}) // 该槽位等待后端应答
    } else {
        slots = append(slots, localSlot{reply: frame.Reply}) // 原位置本地应答
    }
}
if err != nil {
    // frames 中仍包含最后一个 Fatal 帧的本地错误应答。
    // 处理完 frames 后关闭客户端连接，避免对不可信边界重新同步。
}
```

`Frame` 与请求一一对应。转发循环按顺序为每个 Frame 建立一个应答槽位：放行帧写后端并等待后端应答，拒绝帧使用 `Reply`；按槽位顺序回写即可保证流水线应答顺序不变。`Frame.Fatal` 表示协议边界已不可信，必须停止转发并关闭连接。

## 安全行为

- 批量字符串严格按声明的字节数消费并校验尾部 CRLF；长度与实际 UTF-8 字节数不一致时拒绝，不会扫描到下一个 CRLF 来自行“修复”边界。
- 所有放行参数（包括命令名）都重新编码为 `$<实际字节数>\r\n...\r\n`，参数中的 CRLF、RESP 片段和中文都只是数据。
- 内联命令按 Redis 的引号/反斜杠规则拆分（支持 `\r`、`\n` 和 `\xHH`），随后同样转换为批量字符串数组。
- 数组嵌套使用迭代方式扫描并受 `MaxDepth` 限制；超限请求本地报错后可继续解析后续流水线。
- 拒绝应答固定为 `-ERR ...\r\n`，不会回显攻击者控制的命令或参数。

默认限制：深度 8、单批量字符串 16 MiB、参数数 65536、内联行 64 KiB、未消费请求 64 MiB，可通过 `WithMaxDepth`、`WithMaxBulkLength`、`WithMaxArguments`、`WithMaxInlineLength`、`WithMaxRequestSize` 调整。
