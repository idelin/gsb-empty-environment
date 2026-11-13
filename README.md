# MLLP帧界定符走私与消息拆分

医疗信息系统用 MLLP 在 TCP 上传 HL7 消息，每条消息以 0x0B 开头、0x1C 0x0D 结尾，攻击者在 HL7 字段值里嵌入 0x1C 0x0D 与 0x0B，让接收方把一条消息拆成两条，后半条被当作另一个发送方的新消息。为Go消息网关写可导入的 MLLP 帧读写包：读取侧按块接收字节，块边界可落在任意位置，结果须与整块输入一致；帧外出现的任何字节（不是 0x0B 起始）一律按协议错误并断开；帧内再次出现 0x0B 视为错误；0x1C 之后必须紧跟 0x0D，否则整个帧作废；单帧超过调用方上限时作废，并跳到下一个合法起始；写出侧对消息正文含 0x0B 或 0x1C 时拒绝，不得转义放行，0x0D 作为段分隔符仅允许出现在段末；HL7 转义序列 \X0B\ 与 \X1C\ 解码后得到这些字节时，在入库字段处拒绝。用字段内嵌 0x1C 0x0D、帧内第二个 0x0B、跨块切开的结束序列、转义序列还原和超长帧验证，并枚举全部切块位置。

## 用法

```go
import netsec "example.org/netsec/q0786"

r := netsec.NewReader(conn, 1<<20) // 1 MiB 正文上限；不传或传 0 表示不限制
for {
	body, err := r.ReadFrame()
	if err != nil {
		// ErrProtocol/ErrOutsideFrame/ErrNestedStart：关闭连接。
		// ErrFrameTooLong/ErrInvalidTerminator：该帧已作废，可继续读取下一帧。
		break
	}
	// 写入字段前先解码并检查转义。
	if _, err := netsec.DecodeField(field); err != nil {
		// 字段含原始控制字节，或 \X0B\、\X1C\ 等转义还原出控制字节。
	}
	_ = body
}

w := netsec.NewWriter(conn)
if err := w.WriteFrame(body); err != nil {
	// 正文含 0x0B/0x1C，或 0x0D 不是合法的段分隔符。
}
```

`ReadFrame` 返回不含 `0x0B` 与 `0x1C 0x0D` 的正文。正文上限按两界定符之间的字节数计算。帧外杂散字节、帧内第二个 `0x0B`、以及 `0x1C` 后不是 `0x0D` 的终止序列，都会使 Reader 永久失败；超长帧作废后会在下一个 `0x0B` 处重新同步。`WriteFrame` 在写入任何字节之前完成校验，因此被拒绝的消息不会留下半个帧。
