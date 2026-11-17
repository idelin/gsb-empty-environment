# 早期提示身份隔离

代理转发103提示时，不能让上一个请求的资源提示进入下一个租户响应。写供Go响应管线调用的多阶段响应包，提示与最终响应绑定请求实例，最终响应到达后拒绝迟到提示；仅允许白名单提示字段，不把提示中的认证相关字段合并到最终头。用交错请求及取消测试每个接收者的实际头序列。

## 用法

每个下游请求创建一个独立的 `MultiStageResponse`。该句柄是请求实例能力凭据：
提示、最终响应和取消都只能作用于这个句柄，因此旧请求的迟到提示无法写入新请求。

```go
resp, err := netsec.NewHTTPResponse(r.Context(), requestID, w)
if err != nil {
	log.Fatal(err)
}

// 只保留 Link / Alt-Svc 等白名单字段；认证字段会被丢弃。
if err := resp.Hint(http.Header{
	"Link":          {"</app.css>; rel=preload; as=style"},
	"Authorization": {"Bearer upstream-secret"},
}); err != nil {
	log.Printf("early hint dropped: %v", err)
}

// 最终头来自 finalHeader（以及 ResponseWriter 上原有的非提示头），绝不合并提示头。
if err := resp.Final(http.StatusOK, finalHeader); err != nil {
	log.Fatal(err)
}
```

最终响应一旦提交，`Hint` 返回 `ErrLateHint`（同时匹配 `ErrFinalized`），
不再调用 emitter。请求上下文取消或调用 `Cancel` 后，`Hint` 和 `Final`
均返回 `ErrCanceled`，且只影响该请求。

`HintPolicy` 的默认白名单为 `Link`、`Alt-Svc`。可以通过
`netsec.WithHintPolicy` 扩大非敏感字段，但 `Authorization`、`Cookie`、
`Set-Cookie`、`WWW-Authenticate` 等认证字段始终无法加入白名单。

`HTTPEmitter` 在写 103 时临时只暴露过滤后的提示字段，随后恢复
`ResponseWriter` 原有头，避免响应管线中的其他头被带入 103；最终响应使用
`Final` 传入的头，不会回填任何提示头。`netsec/http_test.go` 用交错请求和
取消场景逐次录制每个接收者的 `WriteHeader` 头快照，校验实际头序列。
