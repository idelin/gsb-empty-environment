# gRPC元数据身份头剥离

网关把验证后的用户身份放进 gRPC 元数据转发给后端，攻击者在入口请求里自带同名元数据去冒充他人，或利用 -bin 键与保留键。为Go的网关写可导入的元数据净化拦截器：入口先删除所有 x-user-id、x-user-roles、x-tenant-id 以及以 x-internal- 开头的键（键名在 gRPC 下为小写，比较不区分大小写），认证通过后再写入验证过的值；grpc- 开头的保留键（grpc-timeout 由框架处理，不在此转发）、`:` 开头的伪头、te、content-type 由客户端提交时一律丢弃；-bin 结尾的键值须是合法 Base64（可无填充），解码后超过 4096 字节丢弃；同键多值时身份键只保留网关写入的那一个；元数据总字节超过 8192 拒绝。测同名伪造、大小写、grpc- 保留键、-bin 非法 Base64。

## 用法

```go
interceptor := netsec.UnaryServerInterceptor(func(ctx context.Context, md metadata.MD) (netsec.Identity, error) {
	return authenticate(ctx, md)
})

server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
```

`SanitizeMetadata` 可单独用于非 gRPC 入口：它先剥离客户端身份与保留键、校验 `-bin` 值并检查 8192 字节上限；认证成功后用 `ApplyIdentity` 写入唯一的身份值。

仓库中的 `internal/grpcshim` 仅用于离线构建和拦截器单测；被其他 Go 模块导入时，`go.mod` 中的本地 `replace` 不生效，会正常解析真实的 `google.golang.org/grpc`。
