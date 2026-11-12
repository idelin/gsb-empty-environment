# DeltaV版本引用与标签授权

WebDAV 版本控制（RFC 3253）的 UPDATE 能把受控资源更新到某个版本，LABEL 能给版本打标签，攻击者可指向别的租户的版本历史，或抢占已有标签。给Go DeltaV 服务写供处理器调用的授权包：UPDATE 请求里的 DAV:version href 必须属于该受控资源自己的版本历史且调用者对该版本有读权限，否则按未找到处理，不泄漏其他历史的存在；LABEL 的 add 在同一历史里标签已存在时失败，set 把标签移到目标版本，remove 对不存在标签失败，且只能作用于有写权限的历史；CHECKIN 只允许已被同一调用者 CHECKOUT 的工作资源。用跨租户版本 href、标签抢占、未检出就检入和合法更新验证。

## 实现

`netsec.Authorizer` 保存版本历史、版本、ACL、标签和检出状态，处理器应在执行协议动作前调用：

- `AuthorizeUpdate(principal, controlledResourceHref, versionHref)`：版本必须属于受控资源本身的历史，调用者必须属于该历史租户，并且持有该版本的读权限。任何解析、归属或读权限失败都返回同一个 `ErrNotFound`。
- `AuthorizeLabel(principal, historyHref, change)`：在写锁内检查历史写权限并原子应用 `LabelAdd`、`LabelSet` 或 `LabelRemove`。标签以版本历史为作用域，无法用别的租户或别的历史中的版本操作。
- `Checkout(principal, workingResourceHref)` / `AuthorizeCheckin(principal, workingResourceHref)`：CHECKIN 只接受当前调用者自己的未消费检出；成功后检出被消费，不能重放。

所有 href 都要求是唯一的规范化 URL（允许绝对 HTTP(S) URL 或相对路径）；带 query 或 fragment 的 href 会被拒绝并按未找到处理。注册与权限授予接口用于服务启动或测试夹具：

```go
auth := netsec.NewAuthorizer()
_ = auth.AddHistory(netsec.History{Href: "/dav/a/history", Tenant: "tenant-a"})
_ = auth.AddControlledResource("/dav/a/doc", "/dav/a/history")
_ = auth.AddVersion("/dav/a/history/v1", "/dav/a/history")
_ = auth.GrantVersionRead(netsec.Principal{Tenant: "tenant-a", Name: "alice"}, "/dav/a/history/v1")
```

处理器根据 `errors.Is` 映射状态码：

```go
switch {
case errors.Is(err, netsec.ErrNotFound):
    // 404
case errors.Is(err, netsec.ErrForbidden):
    // 403
case errors.Is(err, netsec.ErrLabelExists), errors.Is(err, netsec.ErrLabelNotFound):
    // 409，或按 RFC 前置条件映射
case errors.Is(err, netsec.ErrCheckoutRequired), errors.Is(err, netsec.ErrAlreadyCheckedOut):
    // 409/403
}
```

## 安全不变量

- 跨租户、跨历史的版本 href 与完全不存在的版本 href 返回完全相同的错误，不泄露其他历史。
- 即使 ACL 数据中误授予跨租户读权限，租户边界检查仍然优先生效。
- `LabelAdd` 的冲突检查和写入是同一把锁内完成的，并发抢占只有一个成功者。
- `LabelSet` 只允许把标签指向当前历史内的版本；`LabelRemove` 不存在的标签会失败。
- CHECKIN 的未知资源、未检出和他人检出不会返回可区分当前检出者的信息（未知资源仍按 404 处理）。

运行验证：

```sh
go test -race ./...
```
