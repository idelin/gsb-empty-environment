# gRPC反射与管理服务暴露门控

gRPC 服务端把反射、channelz 与健康检查注册在对外端口上，攻击者用反射列出内部接口，再去调用管理服务。为Go的 gRPC 服务端写可导入的服务暴露门控拦截器：按 :path 的服务名（第一个 `/` 与最后一个 `/` 之间）判断，grpc.reflection.*、grpc.channelz.*、grpc.admin.*、grpc.testing.* 与 grpc.health.v1.Health 仅允许来自调用方给出的内部监听标识的连接，对外监听返回 UNIMPLEMENTED（不是 PERMISSION_DENIED，避免暴露其存在）；路径必须形如 /服务/方法，含多余段、百分号编码、空服务名或大小写变体（如 /GRPC.REFLECTION...）一律返回 UNIMPLEMENTED；流式方法同样拦截，拒绝发生在读取第一条消息之前。测外部连接访问反射、大小写变体、流式调用、内部放行。

## 用法

```go
gate := netsec.NewGate("internal")              // 内部监听标识（可多个）
srv := grpc.NewServer(gate.ServerOptions()...)  // 装配一元+流式门控拦截器

go srv.Serve(gate.WrapListener(internalLis, "internal")) // 内部监听：管理服务可用
go srv.Serve(gate.WrapListener(publicLis, "public"))     // 对外监听：管理服务不可见
```

- 外部连接访问 `grpc.reflection.*`、`grpc.channelz.*`、`grpc.admin.*`、`grpc.testing.*`、`grpc.health.v1.Health` 时得到 `UNIMPLEMENTED`，与未知服务一致，不暴露服务存在性。
- 未经 `WrapListener` 标记的连接按外部处理（fail closed）。
- 非法 `:path`（多余段、百分号编码、空服务名等）与大小写变体一律 `UNIMPLEMENTED`。
- 流式方法在读取第一条消息之前即被拒绝。
- 已有其他拦截器时，用 `grpc.ChainUnaryInterceptor` / `grpc.ChainStreamInterceptor` 组合 `gate.UnaryServerInterceptor()` / `gate.StreamServerInterceptor()`。

## 测试

```sh
go mod tidy   # 首次需要联网拉取 google.golang.org/grpc
go test ./...
```
