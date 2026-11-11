# termfilter

运维平台把远端程序的输出直接送到浏览器终端，恶意输出可以借OSC 52写剪贴板、改窗口标题或伪造超链接。给Go会话代理实现转义序列过滤包：增量解析CSI、OSC、DCS、APC和C1控制字符，序列可能在任意字节处被切开；保留SGR颜色和光标移动，删除其余危险序列，未终止的序列超过上限时截断；不能破坏UTF-8多字节字符。枚举切分位置，与一次性过滤的结果对比。

## 策略

- **保留**：SGR 颜色/属性（CSI `m`，含 256 色和真彩色）、基本光标移动与清屏（`A B C D E F G H d f J K \``）、C0 控制字符与 DEL。
- **删除**：OSC（含 OSC 52 剪贴板、OSC 0/2 标题、OSC 8 超链接）、DCS、APC、PM、普通 ESC 序列、私有模式 CSI（`?`/`>`/`=`/`<` 前缀）、白名单外的 CSI、独立 C1 控制字符（0x80–0x9F 中的非引入符）。
- **长度上限**：序列超过 `MaxSequenceBytes`（默认 1024）即截断，CSI 停止缓冲并吞到终止字节，字符串序列只计数不拷贝、继续吞到 ST/BEL。
- **UTF-8**：不完整的多字节 rune 跨 `Write` 缓冲、完整后一次性输出；非法字节逐字节透传，不拆分合法 rune。

## 用法

```go
var downstream io.Writer // 浏览器终端连接
w := termfilter.NewWriter(downstream)
// 也可 termfilter.NewWriter(downstream, termfilter.MaxSequenceBytes(512))

io.Copy(w, remoteConn) // 任意分片的远端输出
w.Flush()              // 连接结束后丢弃残留的未终止序列
```

一次性过滤（参考实现）：

```go
safe := termfilter.Filter(output)
```

## 测试

- `TestFilterPolicy`：各序列类型的保留/删除策略。
- `TestEverySplitMatchesFilter`：枚举语料的每个切分位置，两段式输入必须与 `Filter` 逐字节一致。
- `TestByteByByteMatchesFilter` / `TestRandomChunkingMatchesFilter`：逐字节与随机分块等价性。
- `TestUTF8NeverSplitAcrossWrites` 等：UTF-8 不被切断、`Flush` 语义、长度上限截断与恢复。
- `FuzzSplitEquivalence` / `FuzzLimitEquivalence`：随机字节流的切分等价性模糊测试。
