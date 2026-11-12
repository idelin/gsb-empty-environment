# gRPC反射与管理服务暴露门控

gRPC 服务端把反射、channelz 与健康检查注册在对外端口上，攻击者用反射列出内部接口，再去调用管理服务。为Go的 gRPC 服务端写可导入的服务暴露门控拦截器：按 :path 的服务名（第一个 `/` 与最后一个 `/` 之间）判断，grpc.reflection.*、grpc.channelz.*、grpc.admin.*、grpc.testing.* 与 grpc.health.v1.Health 仅允许来自调用方给出的内部监听标识的连接，对外监听返回 UNIMPLEMENTED（不是 PERMISSION_DENIED，避免暴露其存在）；路径必须形如 /服务/方法，含多余段、百分号编码、空服务名或大小写变体（如 /GRPC.REFLECTION...）一律返回 UNIMPLEMENTED；流式方法同样拦截，拒绝发生在读取第一条消息之前。测外部连接访问反射、大小写变体、流式调用、内部放行。

## 实现

可导入包位于 `svcgate`：

- `svcgate/pure.go`：无 gRPC 依赖的路径解析与服务匹配规则。
- `svcgate/gate.go`：unary/stream 拦截器、内部监听标识与传输凭据包装。
- `svcgate/gate_integration_test.go`：双 TCP 监听的端到端测试。

### 门控规则

- 服务名取 `:path`（即 `info.FullMethod`）第一个 `/` 与最后一个 `/` 之间的文本，且路径必须严格形如 `/服务/方法`：缺少前导 `/`、空服务名、空方法名、多余段（多个 `/`）以及包含 `%` 的百分号编码路径一律返回 `codes.Unimplemented`。
- 服务匹配大小写敏感：`grpc.reflection.`、`grpc.channelz.`、`grpc.admin.`、`grpc.testing.` 前缀，以及精确名 `grpc.health.v1.Health` 被门控；`/GRPC.REFLECTION...` 这类大小写变体不匹配门控规则，会按未知服务走 grpc-go 默认路径，同样得到 `codes.Unimplemented`。
- 外部连接访问被门控服务返回固定错误 `codes.Unimplemented, "unknown service"`，与真正不存在的服务无法区分，不使用 `PermissionDenied`。
- 流式拦截在 `StreamServerInterceptor` 创建流时执行，早于 handler 与第一条 `RecvMsg`；测试通过 handler 调用计数断言拒绝发生在读首条消息之前。

### 内部监听标识

内部身份基于连接而不是方法参数传播：

1. `InternalListener(l net.Listener)` 包装内部监听，给每个 Accept 的连接打标。
2. `GateCredentials(base)` 包装服务端传输凭据（明文或 TLS 均可），握手时把连接标记转成 peer 上的 `InternalAuthInfo`；TLS 场景原始 `credentials.TLSInfo` 通过 `Underlying()` 保留。
3. 拦截器用 `IsInternal(ctx)` 读取 peer 标识；外部监听不包装，因此不带标识。

### 接入示例

```go
creds := svcgate.GateCredentials(insecure.NewCredentials())
opts := append(svcgate.ServerOptions(), grpc.Creds(creds))
srv := grpc.NewServer(opts...)

externalLis, _ := net.Listen("tcp", ":9090")
internalLis, _ := net.Listen("tcp", "127.0.0.1:9091")

go srv.Serve(externalLis)                    // 外部：反射/健康等返回 Unimplemented
go srv.Serve(svcgate.InternalListener(internalLis)) // 内部：完整放行
```

## 测试

- `svcgate/gate_pure_test.go`、`svcgate/gate_test.go`：路径解析与门控匹配的纯函数测试，无需网络或 gRPC 依赖。
- `svcgate/gate_integration_test.go`：外部连接访问反射/健康检查、大小写变体、畸形路径、流式调用在读首条消息前被拒，以及内部监听放行反射、健康检查、流式与普通服务。

当前沙箱无外网且模块缓存为空，集成测试需要在可访问 module proxy 的环境执行：

```sh
go mod tidy
go test ./...
```
