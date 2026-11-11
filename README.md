# hartransport

爬虫测试依赖线上站点，结果不稳定。给Go爬虫实现可传给http.Client的HAR回放Transport：按方法、规范化URL、选定请求头和请求体匹配记录，同一请求多次出现时按顺序消费，重定向和Set-Cookie按记录生效；未匹配时返回含最接近候选及差异的错误。录制端写成RoundTripper包装并输出合法HAR，两端做往返测试。

## 用法

### 录制（线上/集成环境）

`Recorder` 是一个 `http.RoundTripper`，包装真实 Transport 并记录每次请求-响应：

```go
rec := &hartransport.Recorder{Next: http.DefaultTransport}
client := &http.Client{Transport: rec}
resp, _ := client.Get("https://example.com/products")
resp.Body.Close()

f, _ := os.Create("crawl.har")
defer f.Close()
_ = rec.WriteHAR(f) // 合法 HAR 1.2 JSON
```

### 回放（离线测试）

`ReplayTransport` 同样是 `http.RoundTripper`，直接喂给 `http.Client`，不发起真实网络请求：

```go
rt, err := hartransport.NewReplayTransportFromFile("crawl.har")
if err != nil { panic(err) }
// 可选：限定参与匹配的请求头（默认见 DefaultMatchHeaders）
rt.MatchHeaders = []string{"Content-Type", "Authorization"}

client := &http.Client{Transport: rt}
resp, err := client.Get("https://example.com/products")
```

## 匹配规则

- **方法**：HTTP method 精确匹配。
- **规范化 URL**：scheme/host 小写、去掉默认端口、空路径补 `/`、丢弃 fragment、query 参数排序。
- **选定请求头**：通过 `MatchHeaders` 配置（默认 `DefaultMatchHeaders`），头部名大小写不敏感；请求和记录中都不存在的头视为相等。
- **请求体**：按字节精确匹配；二进制体在 HAR 中以 base64 存储并自动还原。
- **顺序消费**：同一请求在 HAR 中出现多次时，按录制顺序逐条消费。
- **重定向 / Set-Cookie**：响应按记录原样重建（保留 `Location` 和多个 `Set-Cookie`），配合 `http.Client` 的 cookie jar 与重定向策略按记录生效。

## 未匹配错误

无可用记录时返回 `*MatchError`，包含：

- 最接近的候选条目及其下标（差异字段数最少；同分时优先已消费的完全匹配）；
- 每个差异字段（`method` / `url` / `header:<Name>` / `body`）的期望值与实际值；
- `AlreadyConsumed` 标记：最接近的候选是否已被消费（即同一请求出现次数超过录制次数）。

## 测试

```sh
go test -race ./...
```

测试不监听端口，用内存中的 RoundTripper 模拟服务端，覆盖 GET/POST、二进制体、
重定向+Cookie、顺序消费、并发回放、匹配错误、HAR 序列化往返。
