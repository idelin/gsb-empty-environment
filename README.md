# 响应正文边界核验包

前置代理与源站对响应正文结束位置不同，会把攻击者内容当作下一租户响应。本包用于 Go 代理测试宿主：输入请求方法队列和原始响应字节，用题内规定策略与调用方提供的另一解析策略逐响应比较“所属请求 + 字节区间”，在最早出现跨请求归属差异时阻断整条连接，并保留两侧完整证据。

## 题内边界规则

- `1xx` 信息响应无正文，且不消耗请求；后续最终响应仍属于同一请求。
- `HEAD` 与 `204` 响应无正文，即使带 `Content-Length` 或 `Transfer-Encoding`。
- 其他响应只允许由唯一十进制 `Content-Length` 或连接关闭定界。
- 拒绝 chunked、重复或冲突的 `Content-Length`、`Content-Length` 与
  `Transfer-Encoding` 同时出现，以及未达到声明长度的提前关闭。

## 快速使用

```go
methods := []string{"GET", "GET"}
raw := []byte(
    "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello" +
        "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nworld",
)

// 省略回调时使用内置的 ParseLenient。
report := taskpkg.Verify(methods, raw)

// 或传入任意受支持的解析回调。
report = taskpkg.Verify(methods, raw, taskpkg.ParseLenient)
```

`Verify` 返回 `Report`：

- `Earliest`：按字节偏移最早的一处差异，包含两侧请求号、响应号、区间和原始字节。
- `Differences`：按字节顺序排列的其余差异。
- `Findings`：切块、重复长度、提前关闭、信息响应、无主响应等枚举结果。
- `Strict` / `Alternate`：两侧完整的 `ParseResult`，包括全部响应、错误和原始流副本。
- `CrossRequest`：是否出现跨请求归属差异。
- `Blocked` / `Verdict`：连接级结论；跨请求差异、严格策略拒绝或无主响应都会阻断。

另提供 `Run` / `Check` / `Audit` / `Compare` / `CheckBoundaries` 等入口，方法队列与原始字节参数顺序可互换。回调支持 `func([]string, []byte) ParseResult`、`([]Response, error)`、`([]Segment, error)` 及常见变体。

## 验证

```sh
go test -race ./...
go vet ./...
```
