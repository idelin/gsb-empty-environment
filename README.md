# 私有页面被静态扩展名骗进了共享缓存

CDN按`/profile`是否带`.css`判断可缓存，攻击者请求`/profile/secret.css`让服务器返回用户私有资料并被共享缓存存储。请实现供Go边缘缓存调用的可导入可缓存性判定组件。缓存决定依据响应的Cache-Control、Vary、Set-Cookie、Authorization与状态码，而不是路径扩展名；带私有标记或用户上下文的响应一律不进入共享缓存。请求的Accept与扩展名不改变判定，缓存键包含必要的协商头。命中前再次核对当前请求的凭据上下文。用静态扩展名欺骗、无扩展名私有页、Vary缺失、带凭据请求和合法公开资源验证私有内容不被共享缓存返回。

## 实现

`netsec` 包供边缘缓存直接导入，判定完全不依赖路径扩展名：

- `Decide(req, status, headers)`：依据请求方法、`Authorization`/`Cookie`
  凭据，以及响应的 `Cache-Control`、`Vary`、`Set-Cookie`、状态码给出
  `Decision`；`no-store`、`private`、`Set-Cookie`、`Vary: *` 与带凭据但
  未显式 `public` 的响应一律 `Storable=false`。
- `SharedCache.Store/Lookup`：仅保存通过判定的响应；`Vary` 协商头进入
  变体键，命中前用 `CredentialFingerprint` 再次核对当前请求凭据，匿名
  用户与其他用户都不会拿到凭据绑定的响应。
- `PrimaryKey`/`VariantKey`：主键由方法、协议、主机、路径与查询构成，
  `Accept` 与扩展名不参与可缓存性判定；只有响应声明的 `Vary` 头才进入键。
- `no-cache`、`max-age=0`、`must-revalidate` 的响应可保存但命中时返回
  `MustRevalidate`，要求回源确认。

## 验证

```sh
go test -race ./...
```

测试覆盖静态扩展名欺骗、无扩展名私有页、Vary 缺失/通配、带凭据请求
（含跨用户与匿名命中复核）以及合法公开资源的正常缓存。
