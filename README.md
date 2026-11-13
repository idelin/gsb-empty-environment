# 期望续传夹带正文

Expect为100-continue时，客户端提前把第二个请求放进首段正文。在发送100响应前，只缓冲不超过配置上限的正文；看到完整的第二个请求行则失败。首段正文短且没有请求行时继续。协议网关调用可导入的Go解码器做这次裁决。在未收到100之前写入一个新的请求行。

## 使用方法

网关解析完请求头后，如果请求带有 `Expect: 100-continue`，先读取当前已经到达的正文前缀并交给
`netsec.Decoder` 裁决。在裁决完成前不要发送 `100 Continue`，也不要把该段正文转发给上游。

```go
decoder := netsec.NewDecoder(netsec.Config{MaxBody: 8 << 10})

// earlyBody 是发送 100 Continue 之前已经从连接读到的正文。
if err := decoder.Decode(earlyBody); err != nil {
	if errors.Is(err, netsec.ErrSmuggledRequest) {
		// 首段正文里出现了完整的第二个请求行，拒绝请求并关闭连接。
	}
	// 其他错误同样终止本次请求。
}

// 未被拒绝时，Buffered 只包含不超过 MaxBody 的前缀，可继续按原请求处理。
prefix := decoder.Buffered()
```

也可以直接按 `http.Header` 判断是否需要检查：

```go
if netsec.HasExpectContinue(req.Header) {
	err := netsec.Screen(req.Header, earlyBody, netsec.Config{MaxBody: 8 << 10})
	// err == netsec.ErrSmuggledRequest 时不得发送 100 Continue。
}
```

## 裁决规则

- 只保留不超过配置上限的正文前缀；上限之外的字节不参与检查。
- 在行首识别完整的 `method SP request-target SP HTTP-version` 请求行。以 `LF` 或 `CRLF`
  结束的完整行都会被视为请求行，避免宽松解析器造成差异。
- 请求行尚未完整到达、或者短正文中没有请求行时返回 `nil`，网关可以继续正常处理。
- 一旦发现请求行，后续 `Feed`、`Write`、`Decode` 都返回 `ErrSmuggledRequest`，直到调用
  `Reset`。

流式调用可使用 `NewDecoder`、`Feed`（或 `Check`）、`Write`、`Decode(io.Reader)`。从连接读取时
应设置合适的读超时，`Decode(io.Reader)` 会在达到上限或读到 EOF 时返回。
