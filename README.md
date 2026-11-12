# Early Hints缓存投毒隔离

共享缓存若把 103 Early Hints 当作最终响应缓存，或把 103 里攻击者可控的 Link 头带进缓存条目，就会让后续用户去预加载攻击者的资源。为Go反向代理缓存层写可导入的 103 处理包：103 只向 HTTP/1.1 及以上客户端连接转发，对 HTTP/1.0 客户端丢弃；103 永不写入缓存，也不改变已有条目的元数据，只有最终响应进入缓存判定；转发的 103 只保留 Link 头，且仅限 rel 为 preload 或 preconnect、目标为 https 并且主机在调用方白名单内的链接，其余头（包括 Set-Cookie 与 Content-Length）一律剥除；同一请求最多转发 3 个 103。用 103 当终态、跨主机 preload、Set-Cookie 夹带、第 4 个 103 和 HTTP/1.0 客户端验证。

## 实现

可导入包路径：`example.org/netsec/q0610/earlyhints`（目录 `earlyhints/`）。

- `Processor`：每请求一个的状态机，喂入上游 1xx 响应（经
  `httptrace.ClientTrace.Got1xxResponse` 观察），返回可转发的净化后 103 头或
  `nil`。协议门控（HTTP/1.1+）、Link 白名单、头剥除、每请求最多 3 个均在此实现。
- `Handler`：开箱即用的 `http.Handler`，串联上游请求、103 转发与
  `Cache`/`CachePolicy`。`Cache` 接口只有 `Lookup`/`Store`，103 路径完全不接触
  缓存，结构上保证 103 既不能写入也不能修改任何缓存条目；仅终态响应参与缓存判定，
  终态若仍是 1xx 则返回 502 且不缓存。
- Link 解析（`link.go`）：RFC 8288 风格，正确处理引号/尖括号内的逗号与分号、
  转义引号；仅放行 `rel` 含 `preload`/`preconnect`、绝对 `https` 目标且主机在
  白名单（小写主机名精确匹配，`cdn.example.com.evil.org` 这类后缀伪装不命中）的条目。

## 验证矩阵（`go test ./...`）

| 场景 | 测试 |
| --- | --- |
| 103 被当作终态响应 | `TestTerminal103IsRejectedAndNeverCached`：返回 502，无 103 转发，缓存零写入 |
| 跨主机 / 非 https / 主机伪装 preload | `TestCrossHostAndInsecurePreloadAreDropped`、`TestForwardsOnlySafeHintsAndStripsOtherHeaders` |
| Set-Cookie / Content-Length 夹带 | `TestSetCookieAndContentLengthNeverReachHints`：103 中仅保留单个 Link 头 |
| 第 4 个 103 | `TestAtMostThreeHintsPerRequest`：仅前 3 个转发，且缓存命中时不再产生任何 103 |
| HTTP/1.0（含 HTTP/0.9）客户端 | `TestHTTP10ClientReceivesNoHints`：丢弃全部 103，终态响应照常返回；HTTP/2 客户端仍可收到 |

另有 `ExampleHandler` 展示外部调用方视角的完整用法。测试不监听任何端口：上游由
`RoundTripper` 测试替身通过真实的 `Got1xxResponse` 钩子投递，适配受限沙箱环境。
运行：`go vet ./... && go test -race ./...`（当前覆盖率 96.9%）。
