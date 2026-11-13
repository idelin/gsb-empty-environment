# 预共享身份下标

selected_identity指向不存在的预共享身份。下标必须落在客户端提供的身份列表内；越界失败。下标0且列表非空时继续核对绑定值。可导入的Go库供TLS栈调用。把下标设成列表长度。

## 用法

```go
identities := []netsec.PSKIdentity{
    {Identity: []byte("ticket-a")},
    {Identity: []byte("ticket-b")},
}

// 客户端只发送了一个身份时，服务端选择 0 是合法的。
if err := netsec.ValidateSelectedIdentity(0, identities); err != nil {
    // 处理非法 selected_identity
}

// selected_identity 等于身份列表长度时越界。
err := netsec.ValidateSelectedIdentity(len(identities), identities)
// errors.Is(err, netsec.ErrInvalidSelectedIdentity) == true
```

需要在允许下标 0 后继续核对绑定值时，可同时传入 binder 列表和期望绑定值：

```go
binders := [][]byte{[]byte("binder-a"), []byte("binder-b")}
err := netsec.ValidateSelectedIdentity(0, identities, binders, binders[0])
```

库同时提供 `CheckSelectedIdentity`、`ValidatePSKSelection` 等兼容名称，以及
`ServerHello`/`ClientHello` 适配入口 `ValidateServerHelloSelectedIdentity`。
