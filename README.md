# gRPC反射与管理服务暴露门控

gRPC 服务端把反射、channelz 与健康检查注册在对外端口上，攻击者用反射列出内部接口，再去调用管理服务。为Go的 gRPC 服务端写可导入的服务暴露门控拦截器：按 :path 的服务名（第一个 `/` 与最后一个 `/` 之间）判断，grpc.reflection.*、grpc.channelz.*、grpc.admin.*、grpc.testing.* 与 grpc.health.v1.Health 仅允许来自调用方给出的内部监听标识的连接，对外监听返回 UNIMPLEMENTED（不是 PERMISSION_DENIED，避免暴露其存在）；路径必须形如 /服务/方法，含多余段、百分号编码、空服务名或大小写变体（如 /GRPC.REFLECTION...）一律返回 UNIMPLEMENTED；流式方法同样拦截，拒绝发生在读取第一条消息之前。测外部连接访问反射、大小写变体、流式调用、内部放行。

## 用法

```go
internalLis, _ := net.Listen("tcp", "127.0.0.1:50051") // 内部监听
externalLis, _ := net.Listen("tcp", "0.0.0.0:8443")    // 对外监听

// 内部监听标识：直接把内部 net.Listener 交给门控。
gate := netsec.NewExposureGate(internalLis)

srv := grpc.NewServer(
	grpc.ChainUnaryInterceptor(gate.UnaryInterceptor()),
	grpc.ChainStreamInterceptor(gate.StreamInterceptor()),
)
reflection.Register(srv) // 仅内部监听可达
// ...注册业务服务（内外均可达）

go srv.Serve(internalLis)
go srv.Serve(externalLis)
```

## 判定规则

- 服务名取 `:path` 第一个 `/` 与最后一个 `/` 之间的部分，精确大小写匹配。
- 受保护：`grpc.reflection.*`、`grpc.channelz.*`、`grpc.admin.*`、`grpc.testing.*` 前缀，以及 `grpc.health.v1.Health` 全名。仅内部监听连接放行，对外返回 `UNIMPLEMENTED`。
- 路径不严格形如 `/服务/方法`（多余段、百分号编码、空服务名、空方法名）或属于受保护服务的大小写变体（如 `/GRPC.REFLECTION...`）：任何连接一律 `UNIMPLEMENTED`。
- 流式方法在调用 handler（即读取第一条消息）之前拒绝。
- 连接来源按服务端本地地址与内部监听地址匹配判定；无法确认来源时按外部处理（fail closed）。需要 grpc-go v1.55+（服务端 peer 提供 `LocalAddr`）。

## 测试

路径判定为纯逻辑（`internal/pathclass`），无外部依赖：

```sh
go test ./internal/pathclass/
```

端到端测试（`gate_test.go`）需要联网拉取 gRPC 依赖：

```sh
go mod tidy   # 生成 go.sum
go test ./...
```
