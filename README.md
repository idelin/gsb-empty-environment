# h2h1

`h2h1` 是面向 Go 反向代理宿主的原始字节级 HTTP/2 解析与 H2→H1 降级安全边界。实现只依赖 Go 标准库中的字节、编码与错误工具，不导入 `net/http`、标准库/`x/net` 的 HTTP/2 或 HPACK 实现。

模块路径：`formal.local/w27-6c713639c7`，包名：`h2h1`。

## 快速使用

```go
import (
	"formal.local/w27-6c713639c7" // 使用时按本地包名 h2h1 调用
)

requests, err := h2h1.ParseH2Requests(rawFrameStream,
	h2h1.WithHeaderTableSize(4096),
	h2h1.WithMaxHeaderBlockSize(1<<20),
)
if err != nil {
	// 关闭连接；不要继续降级或复用 HPACK 状态。
}
for _, req := range requests {
	h1, err := h2h1.RenderH1Request(req)
	if err != nil {
		// 防御性拒绝。
	}
	// h1 只有一个规范请求、一个 Content-Length、无 Transfer-Encoding。
	// 可在转发前再交给后端侧严格解析器验证。
	if _, err := h2h1.ParseH1Request(h1); err != nil {
		// 不应发生。
	}
}
```

同一 HTTP/2 连接的所有请求头块必须共用一个 `HPACKDecoder`/`H2Decoder`，因为静态表之外还有跨帧、跨流的动态表状态。一个解码器不能并发处理同一连接的多条流。

## 主要 API

- `ParseAllFrames` / `ParseFrameBytes` / `ReadFrame` / `ParseFrameStream`：帧头、24 位长度、流 ID、标志和完整负载的原始解析。
- `NewHPACKDecoder` / `Decode`：RFC 7541 静态表、动态表、整数和字符串解码；支持跨 stream 的连续 HPACK 状态。
- `HuffmanDecode` / `HuffmanEncode`：RFC 7541 Appendix B 完整码表及严格 EOS/填充校验。
- `NewH2Decoder` / `Feed` / `FeedBytes` / `Finish`：流状态机、HEADERS/CONTINUATION 连续性、流 ID 单调性、DATA 累积和最终 CL 校验。
- `ParseH2Requests` / `DecodeH2Requests`：一次性解析完整连接帧流并返回全部已验证请求。
- `RenderH1Request` / `DowngradeH2Request` / `SafeH1Request`：只接受已通过语义校验的请求，生成单一规范 HTTP/1.1 请求。
- `ParseH1Request`：后端侧严格解析器，拒绝裸 CR/LF、obs-fold、NUL、Transfer-Encoding、连接级头、CL 冲突和请求尾部残留。
- `GenerateAdversarialCases` / `VerifyDefense` / `RunSelfCheck`：对抗用例与内置回归验证。
- `BuildFrame` / `BuildHeaderFrames` / `BuildRequestFrames` / `EncodeHeaderBlock`：测试与宿主侧构造合法原始 H2 帧和 HPACK 块。

## 安全语义

`ParseH2Requests` 在任一条件不满足时返回错误，不会返回部分可信请求：

- HEADERS 后的 CONTINUATION 必须同流且紧邻，中间出现任何其他帧均拒绝。
- HPACK 动态表大小更新只能出现在首个索引/字面头之前，且不得超过解码器配置的 `SETTINGS_HEADER_TABLE_SIZE`。
- Huffman 字符串拒绝 EOS 符号、非全 1 填充以及超过 7 位的填充。
- 拒绝大写头名、伪头位于普通头之后、重复/缺失 `:method`/`:scheme`/`:path`。
- 拒绝 `Connection`、`Keep-Alive`、`Proxy-Connection`、`Upgrade`、`Transfer-Encoding`，以及非 `trailers` 的 `TE`。
- 拒绝头名或值中的 CR、LF、NUL；头值中的其他控制字节也拒绝。
- 多值 `Content-Length` 必须数值相等；拒绝列表、符号、前导零和越界数值。
- 每个请求最终 DATA 字节数必须精确等于 `Content-Length`；`END_STREAM` 提前或尾部超发都会失败。

`RenderH1Request` 不拼接原始 H2 头块：它重新生成请求行和每个头行，过滤伪头/`Host`/`Content-Length`/`TE`，只输出一个由实际 body 长度计算的 `Content-Length`，从不使用 chunked framing。

## 验证

```sh
go test ./...
go vet ./...
```

内置 `VerifyDefense()` 会同时检查：所有 H2.CL、H2.TE、CRLF/NUL 注入、CONTINUATION 拆分/插帧、HPACK 状态攻击都被拒绝，且三条交错多路复用请求全部经 H2 解码、H1 渲染和 H1 严格解析通过。
