# sessionjar

可直接接入 `http.Client` 的分区 CookieJar，解决爬虫登录续期时多个并行请求串用 Cookie 的问题。

## 隔离维度

- **顶层站点**：按 eTLD+1 式的可注册域名分区（内置常见多级后缀，可用 `WithPublicSuffixes` 扩展）。`site-a.test` 与 `site-b.test` 互不共享 Cookie，跨域跳转也不会泄漏。
- **会话代次**：`Refresh(u)` 为该站点开启新一代，Cookie 存储整体替换。

## Cookie 语义

- host-only 与 Domain Cookie 区分存储；Domain 属性与请求主机不匹配时拒绝。
- Path 精确作用域 + RFC 6265 默认路径推导，请求头按长路径优先排序。
- `Expires` / `Max-Age` 过期与删除（负值或过去时间即删），Secure 限制。

## 续期竞态

`GenerationTransport`（`http.RoundTripper` 包装器）把每个逻辑请求（含整条重定向链，跨站点则分别钉住各站点）钉在发起时的代次上，通过请求 `context` 传递：

1. 请求发出前 pin 住当前代次，只用该代次的 Cookie 生成 `Cookie` 头；
2. 响应的 `Set-Cookie` 按钉住的代次写回，并从响应中剥离，避免 `http.Client` 重复存储；
3. 请求结束 unpin；`Refresh` 后仍在途的旧代分区保留到最后一个 pin 释放，随后删除；
4. 旧代迟到的 `Set-Cookie` 找不到分区即丢弃（`Stats.Dropped` 计数），绝不污染新代。

## 用法

```go
jar := sessionjar.New()
client := &http.Client{
    Jar: jar, // 同时满足标准 http.CookieJar 接口
    Transport: &sessionjar.GenerationTransport{
        Base: http.DefaultTransport,
        Jar:  jar,
    },
}

// 登录续期：旧的在途请求结束后，其迟到响应不会写入新代
newGen := jar.Refresh(loginURL)
```

## 测试

`jar_test.go` 用两个进程内站点（`site-a.test` / `site-b.test`，经自定义 RoundTripper 分发，不依赖真实端口）模拟：

- 跨域跳转的站点隔离，并核对两端实际收到的 `Cookie` 请求头；
- 同名 Cookie 不同 Path 的作用域与排序、host-only 不跨主机；
- 阻塞在途请求 + `Refresh` 的续期竞态，验证迟到旧响应不污染新代；
- 过期/删除、并发刷新（`-race`）与可注册域名推导。

```sh
go test -race -cover ./...
```
