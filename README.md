# 密钥派生的域隔离

Go加密宿主需要可导入的HKDF-SHA256密钥派生包，Extract与Expand按标准定义实现并用标准测试向量校验。应用上下文含租户字节串、用途枚举、协议版本、方向和epoch，各字段长度前缀编码，禁止未知用途、超长字段及空租户。用途只允许加密、认证、票据，方向只允许发送和接收。根密钥只能通过宿主句柄使用，派生缓存键必须包含根代次及全部上下文，根撤销后不能取得新派生句柄，已取得句柄可执行一次后失效；不向插件返回原始密钥。用可观察密码操作回调测试字段边界拼接碰撞、方向混用、跨租户缓存、根轮换和撤销竞争，错误上下文不得触发密码操作。

## 使用方式

```go
host := netsec.NewHost()
root, err := host.InstallRoot(1, rootKey) // 根密钥由宿主导入
if err != nil {
	return err
}

ctx := netsec.Context{
	Tenant:    []byte("tenant-a"),
	Purpose:   netsec.PurposeAuthentication,
	Version:   1,
	Direction: netsec.DirectionSend,
	Epoch:     9,
}

handle, err := host.Derive(root, ctx, 32)
if err != nil {
	return err
}
defer host.RevokeRoot(root)

return handle.Use(func(key *netsec.Key) error {
	tag := key.HMAC(message) // 插件只拿到作用域视图，没有原始密钥字节
	return verifyTag(tag)
})
```

## 安全性质

- `Extract` 和 `Expand` 按 RFC 5869 实现，并由 Appendix A 的 SHA-256 测试向量校验。
- 上下文按 `tenant`、`purpose`、`version`、`direction`、`epoch` 顺序编码；每个字段都带四字节大端长度前缀。
- 空租户、超长租户、未知用途和未知方向在触碰根密钥前被拒绝。
- 派生缓存键包含根 ID、根代次、完整规范上下文和密钥长度。
- 根代次同时作为 HKDF `info` 的首个长度前缀字段参与派生，避免根轮换后意外复用密钥。
- `RotateRoot` 原子安装新根并撤销旧根；撤销后旧根不能取得新句柄。
- 已签发的派生句柄是单次能力：并发调用 `Use` 只有一个成功，返回后密钥材料被清零。
- `Observer` 只接收操作元数据，不接收租户内容或密钥材料；错误上下文不会产生密码操作事件。
