# 批量字段绑定把隐藏角色写进了账户

资料更新接口把请求体直接绑定到用户结构体，攻击者在JSON里加入`role`与`verified`字段，服务端接受后自行提权。请实现供Go账户服务调用的可导入字段绑定组件。每个接口声明可写字段白名单、只读字段与服务器生成字段，绑定只复制白名单内且类型正确的值；未知字段、嵌套对象与大小写变体拒绝或忽略并记录。角色、租户、额度与审计字段只能由服务端流程写入，变更走独立授权。用role注入、嵌套对象、大小写键、空值覆盖和合法资料更新验证敏感字段不可由客户端写。

## 组件结构

- `binder/` 可导入的字段绑定组件：
  - `NewSchema(name, writable, readOnly, serverOnly)` 按接口声明字段策略。
  - `Schema.BindClient(body, dst)` 客户端绑定：仅复制白名单内、非 null 且类型正确的值；未知字段、大小写变体、只读/服务器字段、嵌套对象、空值覆盖全部拒绝并记入 `Result.Rejected` 与审计日志。
  - `Schema.ApplyServer(actor, body, dst, auth)` 服务器字段（角色、租户、额度、审计）的唯一写入通道，必须携带 `Authorizer` 独立授权，任一字段未授权即整体拒绝。
- `account/` 账户服务示例：`User` 模型、`ProfileSchema` 与 `Service.UpdateProfile` / `Service.AdminSetFields`。

## 验证

```sh
go test ./...
```

测试覆盖 role 注入、嵌套对象、大小写键、空值覆盖、合法资料更新、类型不匹配与服务端授权写入。
