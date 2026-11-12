# HSM多租户会话隔离

云 HSM 代理把多个租户的会话复用到同一个 slot，PKCS#11 的登录状态按应用共享，攻击者可借他人已登录的会话读取私有对象。为Go代理实现供 PKCS#11 调用转发前使用的会话隔离包：每个租户持有自己的句柄空间，会话对象仅在创建它的会话内可见，令牌对象须在该租户的会话登录后才可见；登录只影响本租户会话，一个租户登出时销毁其全部私有会话对象；对象句柄不得被其他租户猜中，代理对未知句柄与他人句柄返回同一错误。用跨租户句柄猜测、他人登录态、登出清理和合法使用验证。

## 安全模型

- `Isolation` 是整个共享 slot 的隔离域。`TenantID` 必须来自已认证的连接，不能直接使用客户端可控字符串。
- `Session` 是租户私有句柄空间中的一个会话。所有对象句柄由 `crypto/rand` 生成，不使用连续编号；查找句柄时始终限定在当前租户（会话对象还限定在当前会话）。
- 会话对象（`CKA_TOKEN=false`）只对创建它的 `Session` 可见；同租户的其他会话也看不到。
- 令牌对象（`CKA_TOKEN=true`）属于租户。只有该租户登录后，才能创建或读取它；同租户的任一打开会话都可访问。
- `Login` 只设置当前租户的登录态，`Logout` 只清理当前租户的全部会话对象，其他租户不受影响。登出不会删除令牌对象，但令牌对象在重新登录前不可见。
- 未知句柄、已销毁句柄和其他租户的句柄统一返回精确的 `ErrObjectNotFound`，不提供任何存在性/归属 oracle。

## 基本用法

```go
iso := netsec.NewIsolation()

if err := iso.RegisterTenant("acme", "1234"); err != nil { /* ... */ }
sessionA, err := iso.OpenSession("acme")
if err != nil { /* ... */ }
if err := sessionA.Login("1234"); err != nil { /* ... */ }

handle, err := sessionA.CreateTokenObject(netsec.Attributes{
    Class:   netsec.ClassPrivateKey,
    Label:   "signing-key",
    ID:      []byte("key-1"),
    Value:   []byte("key-material"),
    Private: true,
})
if err != nil { /* ... */ }

other, _ := iso.OpenSession("other-tenant")
if _, err := other.GetObject(handle); err != netsec.ErrObjectNotFound {
    // 其他租户永远不应解析出该句柄。
}
```

代理在转发 PKCS#11 调用前，应先通过当前租户的 `Session` 解析外部句柄；只有 `GetObject`/`FindObjects` 等授权成功的对象才能转换成该租户底层会话的真实句柄并继续转发。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖跨租户句柄猜测、他人登录态、登出清理、会话关闭、句柄不可枚举、错误不可区分以及正常创建/查找/销毁流程。
