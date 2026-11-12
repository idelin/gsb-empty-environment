# 早期提示身份隔离

代理转发103提示时，不能让上一个请求的资源提示进入下一个租户响应。写供Go响应管线调用的多阶段响应包，提示与最终响应绑定请求实例，最终响应到达后拒绝迟到提示；仅允许白名单提示字段，不把提示中的认证相关字段合并到最终头。用交错请求及取消测试每个接收者的实际头序列。

## 实现

`package netsec` 提供按请求实例隔离的多阶段响应管线（103 Early Hints + 单个最终响应）。

- `Pipeline`：以 `*http.Request` 身份为键绑定每请求 `Stream`，帧只能送达本请求的 `Sink`，从路由层杜绝跨租户串头。
- `Stream.Hint`：按白名单（默认仅 `Link`，可用 `WithHintWhitelist` 覆盖）过滤提示头；`Authorization`、`Proxy-Authorization`、`Cookie`、`Set-Cookie`、`Www-Authenticate`、`Proxy-Authenticate` 即使被误加白名单也强制剔除，且不修改调用方传入的 map。
- `Stream.Final`：最终头只来自传入参数（克隆后下发），与提示头走完全独立的 map，任何提示字段都不会合并进最终响应。
- 生命周期：最终响应后迟到提示返回 `ErrLateHint`，重复 Final 返回 `ErrFinalized`；`Cancel`（适配层在请求上下文结束时自动调用）后所有帧返回 `ErrStreamClosed` 并解绑。
- `Pipeline.Handler`：net/http 适配中间件，`WriteHeader(103)` 发送净化后的提示，处理器未写状态时自动以 200 收尾；`StreamOf(w)` 可取回流直接发提示。

## 测试

- `pipeline_test.go`：白名单/认证字段剔除、迟到提示拒绝、提示与最终头不合并、三租户交错隔离、取消语义，以及 24 路并发交错的 `-race` 测试，逐接收者校验完整帧序列。
- `http_adapter_test.go`：用 `net.Pipe` 内存连接驱动真实 `http.Server`，按原始 HTTP/1.1 字节断言每个接收者看到的 103/200 帧顺序、认证字段不出现在任何帧、连接交错不串租户、取消后不再产生帧。

运行：`go test -race ./...`
