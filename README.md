# SSE事件字段注入过滤

攻击者提交的消息文本含换行时，可向事件流注入event、id和retry字段。为Go推送服务实现可导入的SSE写出组件：event名只允许登记字符集，data按CR、LF、CRLF全部拆成多行data字段，id不得含NUL或换行，retry由服务端常量决定；序列化结果交WHATWG规则的参考解析回读，须得到原消息。用混合换行、BOM和伪造字段样例测试。

## 实现

可导入包位于 `sse/`（导入路径 `example.org/netsec/q0093/sse`），核心 API：

- `sse.NewWriter(w io.Writer, cfg sse.Config) (*sse.Writer, error)`：登记事件名字符集（默认 `sse.DefaultEventNameCharset`，可通过 `Config.AllowedEventNames` 自定义）与服务端重连间隔（`Config.Retry`，零值取 `sse.DefaultRetry = 30s`）。
- `(*sse.Writer).WriteEvent(name, id, data string) error`：写出一个事件，所有校验在任何字节落盘前完成，拒绝时不产生半帧。

安全保证：

- `name` 逐字节比对登记字符集，越界即返回 `ErrEventNameNotAllowed`；字符集本身含 NUL/CR/LF 时构造失败（`ErrInvalidCharset`）。
- `data` 按 CR、LF、CRLF 统一拆行，每行输出一个 `data:` 字段，换行无法逃逸为字段分隔符。
- `id` 含 NUL、CR、LF 时返回 `ErrInvalidID`。
- `retry:` 仅由 `Config.Retry` 决定，在首个事件前惰性输出一次，调用方无法通过 `WriteEvent` 影响。

回读语义遵循 WHATWG「Interpreting an event stream」：`sse/parser_test.go` 是该规范算法的直接转写（UTF-8 替换解码、BOM 剥离、CR/LF/CRLF 切行、字段处理与空行派发）。规范本身会把 data 中的 CR/CRLF 归一化为 LF，这是 SSE 线格式唯一无法原样保留的字节；其余字节（NUL、U+FEFF、前导空格/冒号等）均原样回读。

测试覆盖（`go test ./...`）：

- 混合换行（LF/CR/CRLF 与尾部空行）的逐字节线格式断言与回读；
- data 内 BOM 原样保留、流首 BOM 被规范剥离；
- 伪造 `event:`/`id:`/`retry:`/注释字段样例无法逃逸出 `data:`；
- 事件名、ID、自定义字符集的拒绝路径，以及拒绝零字节写入；
- `FuzzWriterRoundTrip` 模糊测试（已通过约 190 万次执行）持续校验帧不变量与 WHATWG 回读等价性。

最小用法：

```go
w, _ := sse.NewWriter(rw, sse.Config{Retry: 5 * time.Second})
_ = w.WriteEvent("push.notify", "evt-1", userText)
w.Flush() // rw 实现 http.Flusher 时生效
```
